# Sidebar Inboxes Unread Count Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the account-wide unread inbound-message count beside the Sidebar's Inboxes entry, using the same accent pill as Pending and displaying `99+` for larger totals.

**Architecture:** Add one SWR hook that lists the user's inboxes, fetches each existing per-inbox unread rollup in parallel, and projects those results into a capped account total. Give that aggregate its own cache key, refresh it with the existing 15-second unread polling policy, and invalidate it whenever the current per-inbox unread cache is invalidated. Sidebar remains presentation-only and selects either the Inboxes aggregate or Pending count for the existing badge treatment.

**Tech Stack:** Next.js 16, React 19, TypeScript, SWR 2, Jest, React Testing Library

---

## Task 1: Add the account-wide unread aggregate and cache contract

**Files:**
- Create: `web/src/app/components/hooks/useUnreadCount.ts`
- Create: `web/src/app/components/hooks/useUnreadCount.test.tsx`
- Modify: `web/src/lib/swrKeys.ts`
- Modify: `web/src/lib/swrKeys.test.ts`

- [ ] **Step 1: Write the failing aggregate-hook tests**

Add `web/src/app/components/hooks/useUnreadCount.test.tsx` with mocked API helpers and SWR. Pin all material states: loading, aggregation, cap propagation, empty inboxes, and retention after a transient revalidation error.

```tsx
import useSWR from "swr";
import { accountUnreadKey } from "../../../lib/swrKeys";
import { unreadPolling } from "../../../lib/livePolling";
import {
  getInboxUnread,
  listAgents,
  UNREAD_BADGE_CAP,
} from "../onboarding/api";
import { loadUnreadCount, useUnreadCount } from "./useUnreadCount";

jest.mock("swr", () => ({
  __esModule: true,
  default: jest.fn(),
}));
jest.mock("../onboarding/api", () => ({
  listAgents: jest.fn(),
  getInboxUnread: jest.fn(),
  UNREAD_BADGE_CAP: 99,
}));

const mockUseSWR = useSWR as jest.MockedFunction<typeof useSWR>;
const mockListAgents = listAgents as jest.MockedFunction<typeof listAgents>;
const mockGetInboxUnread = getInboxUnread as jest.MockedFunction<
  typeof getInboxUnread
>;

beforeEach(() => jest.clearAllMocks());

it("loads every inbox in parallel and aggregates unread messages", async () => {
  mockListAgents.mockResolvedValue([
    { email: "a@example.com" },
    { email: "b@example.com" },
  ] as Awaited<ReturnType<typeof listAgents>>);
  mockGetInboxUnread
    .mockResolvedValueOnce({ count: 2, more: false })
    .mockResolvedValueOnce({ count: 3, more: false });

  await expect(loadUnreadCount()).resolves.toEqual({ count: 5, more: false });
  expect(mockGetInboxUnread).toHaveBeenCalledTimes(2);
});

it("caps the total and preserves the more-than-cap signal", async () => {
  mockListAgents.mockResolvedValue([
    { email: "a@example.com" },
    { email: "b@example.com" },
  ] as Awaited<ReturnType<typeof listAgents>>);
  mockGetInboxUnread
    .mockResolvedValueOnce({ count: 60, more: false })
    .mockResolvedValueOnce({ count: 50, more: false });

  await expect(loadUnreadCount()).resolves.toEqual({
    count: UNREAD_BADGE_CAP,
    more: true,
  });
});

it("returns zero when the account has no inboxes", async () => {
  mockListAgents.mockResolvedValue([]);
  await expect(loadUnreadCount()).resolves.toEqual({ count: 0, more: false });
  expect(mockGetInboxUnread).not.toHaveBeenCalled();
});

it("subscribes with unread polling and returns null until data exists", () => {
  mockUseSWR.mockReturnValue({ data: undefined } as ReturnType<typeof useSWR>);
  expect(useUnreadCount()).toBeNull();
  expect(mockUseSWR).toHaveBeenCalledWith(
    accountUnreadKey,
    loadUnreadCount,
    unreadPolling,
  );
});

it("keeps the last successful total when revalidation fails", () => {
  mockUseSWR.mockReturnValue({
    data: { count: 4, more: false },
    error: new Error("transient"),
  } as ReturnType<typeof useSWR>);
  expect(useUnreadCount()).toEqual({ count: 4, more: false });
});
```

- [ ] **Step 2: Write the failing cache invalidation test**

Extend `web/src/lib/swrKeys.test.ts` to subscribe to both keys and assert that the existing message-read invalidation path revalidates the per-inbox cache and the new account aggregate while leaving unrelated inboxes alone.

```tsx
import {
  accountUnreadKey,
  agentUnreadKey,
  invalidateAgentUnread,
  invalidateMessageDetail,
  messageDetailKey,
} from "./swrKeys";

it("revalidates the per-inbox and account-wide unread entries", async () => {
  const agentFetcher = jest.fn().mockResolvedValue({ count: 1, more: false });
  const accountFetcher = jest.fn().mockResolvedValue({ count: 1, more: false });
  const otherFetcher = jest.fn().mockResolvedValue({ count: 2, more: false });
  renderHook(() => useSWR(agentUnreadKey("a@example.com"), agentFetcher));
  renderHook(() => useSWR(accountUnreadKey, accountFetcher));
  renderHook(() => useSWR(agentUnreadKey("b@example.com"), otherFetcher));
  await waitFor(() => {
    expect(agentFetcher).toHaveBeenCalledTimes(1);
    expect(accountFetcher).toHaveBeenCalledTimes(1);
    expect(otherFetcher).toHaveBeenCalledTimes(1);
  });

  await act(async () => {
    await invalidateAgentUnread("a@example.com");
  });

  await waitFor(() => {
    expect(agentFetcher).toHaveBeenCalledTimes(2);
    expect(accountFetcher).toHaveBeenCalledTimes(2);
  });
  expect(otherFetcher).toHaveBeenCalledTimes(1);
});
```

- [ ] **Step 3: Run the focused tests and confirm they fail**

Run:

```bash
cd web && npm test -- --runInBand src/app/components/hooks/useUnreadCount.test.tsx src/lib/swrKeys.test.ts
```

Expected: FAIL because `useUnreadCount`, `accountUnreadKey`, and the aggregate invalidation do not exist.

- [ ] **Step 4: Add the cache key and dual invalidation**

In `web/src/lib/swrKeys.ts`, add the account key beside the existing unread key and make the established helper invalidate both entries:

```ts
export const accountUnreadKey = "account-unread";

export function invalidateAgentUnread(email: string) {
  return Promise.all([
    mutate(agentUnreadKey(email)),
    mutate(accountUnreadKey),
  ]);
}
```

This keeps every existing caller in `ThreadBubble.tsx` and trash flows correct without adding parallel mutation APIs.

- [ ] **Step 5: Implement the aggregate hook**

Create `web/src/app/components/hooks/useUnreadCount.ts`:

```ts
import useSWR from "swr";
import { accountUnreadKey } from "../../../lib/swrKeys";
import { unreadPolling } from "../../../lib/livePolling";
import {
  getInboxUnread,
  listAgents,
  UNREAD_BADGE_CAP,
} from "../onboarding/api";

export type UnreadCount = { count: number; more: boolean };

export async function loadUnreadCount(): Promise<UnreadCount> {
  const agents = await listAgents();
  const unread = await Promise.all(
    agents.map((agent) => getInboxUnread(agent.email)),
  );
  const total = unread.reduce((sum, item) => sum + item.count, 0);
  return {
    count: Math.min(total, UNREAD_BADGE_CAP),
    more: total > UNREAD_BADGE_CAP || unread.some((item) => item.more),
  };
}

export function useUnreadCount(): UnreadCount | null {
  const { data } = useSWR(accountUnreadKey, loadUnreadCount, unreadPolling);
  return data ?? null;
}
```

- [ ] **Step 6: Re-run the focused tests**

Run:

```bash
cd web && npm test -- --runInBand src/app/components/hooks/useUnreadCount.test.tsx src/lib/swrKeys.test.ts
```

Expected: PASS.

- [ ] **Step 7: Commit the data-layer slice**

```bash
git add web/src/app/components/hooks/useUnreadCount.ts web/src/app/components/hooks/useUnreadCount.test.tsx web/src/lib/swrKeys.ts web/src/lib/swrKeys.test.ts
git commit -m "feat(web): aggregate sidebar unread count"
```

## Task 2: Render the Inboxes badge consistently with Pending

**Files:**
- Modify: `web/src/app/components/loft/Sidebar.tsx`
- Modify: `web/src/app/components/loft/Sidebar.test.tsx`

- [ ] **Step 1: Write failing Sidebar presentation tests**

Mock `useUnreadCount` beside `usePendingCount`, reset it in `beforeEach`, and pin hidden, numeric, capped, and coexistence behavior:

```tsx
let mockUnreadCount: { count: number; more: boolean } | null = null;
jest.mock("../hooks/useUnreadCount", () => ({
  useUnreadCount: () => mockUnreadCount,
}));

beforeEach(() => {
  mockUnreadCount = null;
});

it("shows the Inboxes unread count only when it is positive", () => {
  mockUnreadCount = { count: 0, more: false };
  const { rerender } = render(<Sidebar />);
  const inboxes = document.querySelector(`a[href="/inboxes"]`);
  expect(inboxes?.querySelector("[data-nav-badge]")).toBeNull();

  mockUnreadCount = { count: 7, more: false };
  rerender(<Sidebar />);
  expect(inboxes?.querySelector("[data-nav-badge]")).toHaveTextContent("7");
});

it("shows 99+ when the aggregate says more unread messages exist", () => {
  mockUnreadCount = { count: 99, more: true };
  render(<Sidebar />);
  const inboxes = document.querySelector(`a[href="/inboxes"]`);
  expect(inboxes?.querySelector("[data-nav-badge]")).toHaveTextContent("99+");
});

it("can show Inboxes and Pending counts at the same time", () => {
  mockUnreadCount = { count: 4, more: false };
  mockPendingCount = 2;
  render(<Sidebar />);
  expect(document.querySelector(`a[href="/inboxes"]`)).toHaveTextContent("4");
  expect(document.querySelector(`a[href="/reviews"]`)).toHaveTextContent("2");
});
```

Also update the existing Pending assertion to select `[data-nav-badge]`; this avoids false positives from unrelated nav text.

- [ ] **Step 2: Run the Sidebar test and confirm it fails**

Run:

```bash
cd web && npm test -- --runInBand src/app/components/loft/Sidebar.test.tsx
```

Expected: FAIL because Sidebar does not consume or render the unread aggregate.

- [ ] **Step 3: Generalize the existing badge rendering**

Import and call `useUnreadCount`, compute one `badgeLabel` per nav item, and reuse the exact existing Pending pill:

```tsx
const unreadCount = useUnreadCount();

const badgeLabel =
  item.href === "/inboxes" && unreadCount && unreadCount.count > 0
    ? unreadCount.more
      ? "99+"
      : String(unreadCount.count)
    : item.href === "/reviews" && pendingCount !== null && pendingCount > 0
      ? String(pendingCount)
      : null;
```

Render `badgeLabel` in the current pill, adding `data-nav-badge` for scoped assertions. Do not introduce new colors, dimensions, animation, or layout behavior: Inboxes and Pending use the same `var(--accent)` background, white bold monospace text, `18px` height, and pill radius.

- [ ] **Step 4: Run the Sidebar test and confirm it passes**

Run:

```bash
cd web && npm test -- --runInBand src/app/components/loft/Sidebar.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Run the full web verification**

Run:

```bash
cd web && npm test -- --runInBand
cd web && npm run lint
cd web && npm run build
```

Expected: all tests pass, ESLint exits 0, and Next.js static export succeeds.

- [ ] **Step 6: Verify the seeded unread message in the running local UI**

Open `http://127.0.0.1:3000/inboxes/messages?email=demo-inbox%40local.test` without selecting the unread message. Confirm:

- Inboxes shows an accent-colored `1` pill matching Pending's visual treatment.
- The unread message remains visually darker in the list.
- Opening that message marks it read and the Inboxes badge clears after invalidation/revalidation.

- [ ] **Step 7: Commit the presentation slice**

```bash
git add web/src/app/components/loft/Sidebar.tsx web/src/app/components/loft/Sidebar.test.tsx
git commit -m "feat(web): show unread count in sidebar"
```

## Final verification

- [ ] Confirm `git status --short` contains only expected work (leave `.superpowers/` untracked and uncommitted).
- [ ] Review the diff against `docs/superpowers/specs/2026-07-22-sidebar-inboxes-unread-count-design.md`.
- [ ] Confirm no placeholders (`TODO`, `TBD`, omitted test bodies) remain in implementation or tests.
- [ ] Confirm the hook, Sidebar mock, and exported cache-key types agree under the production TypeScript build.
