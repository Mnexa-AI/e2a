# Live Inbox Polling and Delivery Status Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make inbox messages, unread badges, and review counts refresh automatically while surfacing attention-aware outbound delivery states in the existing Loft UI.

**Architecture:** Add scoped SWR polling options only to the three live dashboard queries, leaving static resources untouched and preserving focus/reconnect revalidation. Extend the existing message-status component into the canonical review/delivery mapping, then consume it in compact thread rows and outbound conversation messages.

**Tech Stack:** Next.js 16 App Router, React 19, TypeScript, SWR 2, Jest 30, React Testing Library, `@e2a/ui` Loft components.

---

## File map

- Create `web/src/lib/livePolling.ts` — shared polling cadences and SWR option objects.
- Create `web/src/lib/livePolling.test.ts` — exact cadence and hidden/offline behavior contract.
- Modify `web/src/app/components/messages/MessageStatusChip.tsx` — canonical review and delivery status derivation and rendering.
- Modify `web/src/app/components/messages/MessageStatusChip.test.tsx` — exhaustive mapping, precedence, attention, empty, and unknown-state tests.
- Modify `web/src/app/components/messages/ThreadRow.tsx` — compact attention status for the latest outbound message.
- Create `web/src/app/components/messages/ThreadRow.test.tsx` — row-level visibility and latest-message behavior.
- Modify `web/src/app/components/messages/ThreadBubble.tsx` — status chip on every outbound conversation message.
- Modify `web/src/app/components/messages/ThreadBubble.test.tsx` — outbound status rendering and inbound omission.
- Modify `web/src/app/(app)/inboxes/(view)/messages/page.tsx` — 10-second active-inbox polling.
- Modify `web/src/app/(app)/inboxes/(view)/messages/page.test.tsx` — timer-driven inbox refresh test.
- Modify `web/src/app/components/hooks/usePendingCount.ts` — 10-second shared pending-query polling.
- Modify `web/src/app/(app)/reviews/page.tsx` — use the same pending polling options on the shared cache key.
- Modify `web/src/app/(app)/inboxes/_components/AgentCard.tsx` — 15-second unread-probe polling.
- Modify existing pending and AgentCard tests to pin the shared polling configuration.

## Task 1: Canonical outbound status mapping

**Files:**
- Modify: `web/src/app/components/messages/MessageStatusChip.test.tsx`
- Modify: `web/src/app/components/messages/MessageStatusChip.tsx`

- [ ] **Step 1: Replace the status-helper tests with the approved lifecycle contract**

Update `MessageStatusChip.test.tsx` so it asserts the complete mapping and precedence. Keep the existing inbound read/unread cases for backward compatibility:

```tsx
import { deriveStatusChip } from "./MessageStatusChip";

describe("deriveStatusChip", () => {
  test.each([
    ["accepted", "Queued", "info", true],
    ["sending", "Sending", "info", true],
    ["deferred", "Delayed", "warn", true],
    ["failed", "Failed", "danger", true],
    ["bounced", "Bounced", "danger", true],
    ["complained", "Complaint", "danger", true],
    ["sent", "Sent", "success", false],
    ["delivered", "Delivered", "success", false],
  ])(
    "maps delivery_status=%s to %s",
    (deliveryStatus, label, tone, attention) => {
      expect(
        deriveStatusChip({
          direction: "outbound",
          delivery_status: deliveryStatus,
        }),
      ).toEqual({ tone, label, attention });
    },
  );

  it("prioritizes unresolved and rejected review states over delivery", () => {
    expect(
      deriveStatusChip({
        direction: "outbound",
        delivery_status: "sent",
        review_status: "pending_review",
      }),
    ).toEqual({ tone: "warn", label: "Pending review", dot: true, attention: true });

    expect(
      deriveStatusChip({
        direction: "outbound",
        delivery_status: "sent",
        review_status: "review_rejected",
      }),
    ).toEqual({ tone: "danger", label: "Rejected", attention: true });

    expect(
      deriveStatusChip({
        direction: "outbound",
        delivery_status: "sent",
        review_status: "review_expired_rejected",
      }),
    ).toEqual({ tone: "danger", label: "Auto-rejected", attention: true });
  });

  it("lets delivery state override the collapsed review sent value", () => {
    expect(
      deriveStatusChip({
        direction: "outbound",
        delivery_status: "accepted",
        review_status: "sent",
      }),
    ).toEqual({ tone: "info", label: "Queued", attention: true });
  });

  it("falls back to settled review outcomes only without delivery state", () => {
    expect(
      deriveStatusChip({ direction: "outbound", review_status: "sent" }),
    ).toEqual({ tone: "success", label: "Sent", attention: false });
    expect(
      deriveStatusChip({
        direction: "outbound",
        review_status: "review_expired_approved",
      }),
    ).toEqual({ tone: "success", label: "Sent (auto)", attention: false });
  });

  it("shows an unknown delivery state neutrally in detail", () => {
    expect(
      deriveStatusChip({ direction: "outbound", delivery_status: "routing" }),
    ).toEqual({ tone: "neutral", label: "routing", attention: false });
  });

  it("returns null for an outbound message with no lifecycle state", () => {
    expect(deriveStatusChip({ direction: "outbound" })).toBeNull();
  });

  it("preserves inbound read-state mapping", () => {
    expect(
      deriveStatusChip({ direction: "inbound", inbox_status: "unread" }),
    ).toEqual({ tone: "info", label: "Unread", attention: false });
    expect(
      deriveStatusChip({ direction: "inbound", inbox_status: "read" }),
    ).toEqual({ tone: "neutral", label: "Read", attention: false });
  });
});
```

- [ ] **Step 2: Run the helper test and verify the new contract fails**

Run:

```bash
cd web
npm test -- --runInBand src/app/components/messages/MessageStatusChip.test.tsx
```

Expected: FAIL because `delivery_status`, `attention`, null output, and the new labels are not implemented.

- [ ] **Step 3: Implement the canonical mapping in `MessageStatusChip.tsx`**

Replace the current input/spec types and derivation with:

```tsx
import { Chip, Dot, type ChipTone } from "@e2a/ui";

export type MessageStatusInput = {
  direction: "inbound" | "outbound";
  delivery_status?: string;
  review_status?: string;
  inbox_status?: string;
};

export type MessageStatusSpec = {
  tone: ChipTone;
  label: string;
  dot?: boolean;
  attention: boolean;
};

const DELIVERY_STATUS: Record<string, MessageStatusSpec> = {
  accepted: { tone: "info", label: "Queued", attention: true },
  sending: { tone: "info", label: "Sending", attention: true },
  deferred: { tone: "warn", label: "Delayed", attention: true },
  failed: { tone: "danger", label: "Failed", attention: true },
  bounced: { tone: "danger", label: "Bounced", attention: true },
  complained: { tone: "danger", label: "Complaint", attention: true },
  sent: { tone: "success", label: "Sent", attention: false },
  delivered: { tone: "success", label: "Delivered", attention: false },
};

export function deriveStatusChip(
  input: MessageStatusInput,
): MessageStatusSpec | null {
  if (input.direction === "inbound") {
    if (input.inbox_status === "unread") {
      return { tone: "info", label: "Unread", attention: false };
    }
    return { tone: "neutral", label: "Read", attention: false };
  }

  switch (input.review_status) {
    case "pending_review":
      return {
        tone: "warn",
        label: "Pending review",
        dot: true,
        attention: true,
      };
    case "review_rejected":
      return { tone: "danger", label: "Rejected", attention: true };
    case "review_expired_rejected":
      return { tone: "danger", label: "Auto-rejected", attention: true };
  }

  if (input.delivery_status) {
    return (
      DELIVERY_STATUS[input.delivery_status] ?? {
        tone: "neutral",
        label: input.delivery_status,
        attention: false,
      }
    );
  }

  if (input.review_status === "review_expired_approved") {
    return { tone: "success", label: "Sent (auto)", attention: false };
  }
  if (input.review_status === "sent") {
    return { tone: "success", label: "Sent", attention: false };
  }
  return null;
}

export function MessageStatusChip(props: MessageStatusInput) {
  const spec = deriveStatusChip(props);
  if (!spec) return null;
  const dotTone =
    spec.tone === "warn"
      ? "warn"
      : spec.tone === "danger"
        ? "danger"
        : null;
  return (
    <Chip tone={spec.tone}>
      {spec.dot && dotTone && <Dot tone={dotTone} />}
      {spec.label}
    </Chip>
  );
}
```

Remove the obsolete `webhook_status` handling from this component. Webhook
subscriber delivery and SMTP email delivery are separate axes; the inbox chip
must describe the email lifecycle requested by this feature.

- [ ] **Step 4: Run the helper test and verify it passes**

Run:

```bash
cd web
npm test -- --runInBand src/app/components/messages/MessageStatusChip.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit the canonical mapping**

```bash
git add web/src/app/components/messages/MessageStatusChip.tsx web/src/app/components/messages/MessageStatusChip.test.tsx
git commit -m "feat(web): map outbound delivery statuses"
```

## Task 2: Attention-aware status in thread rows and conversations

**Files:**
- Create: `web/src/app/components/messages/ThreadRow.test.tsx`
- Modify: `web/src/app/components/messages/ThreadRow.tsx`
- Modify: `web/src/app/components/messages/ThreadBubble.test.tsx`
- Modify: `web/src/app/components/messages/ThreadBubble.tsx`

- [ ] **Step 1: Add failing `ThreadRow` visibility tests**

Create `ThreadRow.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import { ThreadRow } from "./ThreadRow";
import type { MessageSummary } from "../types";
import type { Thread } from "./threading";

function message(
  id: string,
  overrides: Partial<MessageSummary> = {},
): MessageSummary {
  return {
    id,
    direction: "outbound",
    from: "support@acme.dev",
    to: ["alice@example.com"],
    recipient: "alice@example.com",
    subject: "Status update",
    status: "accepted",
    created_at: `2026-07-19T10:0${id.slice(-1)}:00Z`,
    ...overrides,
  };
}

function thread(messages: MessageSummary[]): Thread {
  const latest = messages[messages.length - 1];
  return {
    key: "conv:status",
    conversationId: "status",
    counterparty: { email: "alice@example.com", name: "Alice" },
    subject: "Status update",
    state: messages.some((m) => m.review_status === "pending_review")
      ? "pending"
      : "active",
    lastMessageAt: latest.created_at,
    startedAt: messages[0].created_at,
    msgCount: messages.length,
    lastDirection: latest.direction,
    lastPreview: latest.subject,
    messages,
  };
}

it("shows attention-worthy status for the latest outbound message", () => {
  render(
    <ThreadRow
      thread={thread([message("1", { status: "accepted" })])}
      active={false}
      onSelect={jest.fn()}
    />,
  );
  expect(screen.getByText("Queued")).toBeInTheDocument();
});

it("omits settled delivery status from the compact row", () => {
  render(
    <ThreadRow
      thread={thread([message("2", { status: "delivered" })])}
      active={false}
      onSelect={jest.fn()}
    />,
  );
  expect(screen.queryByText("Delivered")).not.toBeInTheDocument();
});

it("does not surface a historical failure after a newer inbound message", () => {
  const failed = message("3", { status: "failed" });
  const inbound = message("4", {
    direction: "inbound",
    from: "alice@example.com",
    to: ["support@acme.dev"],
    recipient: "support@acme.dev",
    status: "",
    read_status: "unread",
  });
  render(
    <ThreadRow
      thread={thread([failed, inbound])}
      active={false}
      onSelect={jest.fn()}
    />,
  );
  expect(screen.queryByText("Failed")).not.toBeInTheDocument();
});

it("preserves the thread-level pending signal when pending is historical", () => {
  const held = message("5", { status: "", review_status: "pending_review" });
  const inbound = message("6", {
    direction: "inbound",
    from: "alice@example.com",
    to: ["support@acme.dev"],
    recipient: "support@acme.dev",
    status: "",
  });
  render(
    <ThreadRow
      thread={thread([held, inbound])}
      active={false}
      onSelect={jest.fn()}
    />,
  );
  expect(screen.getByText("Pending")).toBeInTheDocument();
});
```

- [ ] **Step 2: Add failing outbound-status tests to `ThreadBubble.test.tsx`**

Append:

```tsx
describe("ThreadBubble outbound delivery status", () => {
  function outboundMessage(id: string, status: string): MessageSummary {
    return {
      ...msg(id),
      direction: "outbound",
      from: "support@acme.dev",
      to: ["james@x.com"],
      status,
      review_status: "sent",
    };
  }

  it.each([
    ["accepted", "Queued"],
    ["sending", "Sending"],
    ["sent", "Sent"],
    ["delivered", "Delivered"],
    ["failed", "Failed"],
  ])("renders %s as %s", async (status, label) => {
    mockGet.mockResolvedValue({
      direction: "outbound",
      data: { body_text: "outbound body", body_html: "" },
    } as never);
    render(
      <ThreadBubble
        message={outboundMessage(`msg_status_${status}`, status)}
        counterparty={CP}
        agentEmail="support@acme.dev"
      />,
    );
    expect(screen.getByText(label)).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText("outbound body")).toBeInTheDocument());
  });

  it("does not add a delivery chip to inbound messages", async () => {
    inbound({ parsed: { text: "inbound body" }, raw_message: "" });
    render(
      <ThreadBubble
        message={msg("msg_no_delivery_chip")}
        counterparty={CP}
        agentEmail="support@acme.dev"
      />,
    );
    await waitFor(() => expect(screen.getByText("inbound body")).toBeInTheDocument());
    expect(screen.queryByText("Sent")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run both component tests and verify they fail**

Run:

```bash
cd web
npm test -- --runInBand src/app/components/messages/ThreadRow.test.tsx src/app/components/messages/ThreadBubble.test.tsx
```

Expected: FAIL because neither component renders delivery status.

- [ ] **Step 4: Implement latest-message attention status in `ThreadRow.tsx`**

Add imports:

```tsx
import { MessageStatusChip, deriveStatusChip } from "./MessageStatusChip";
```

After `pending`, derive the latest outbound state:

```tsx
const latest = thread.messages[thread.messages.length - 1];
const latestStatus =
  latest.direction === "outbound"
    ? deriveStatusChip({
        direction: "outbound",
        delivery_status: latest.status,
        review_status: latest.review_status,
      })
    : null;
const showLatestStatus = latestStatus?.attention === true;
```

Replace the current pending-pill block with:

```tsx
{showLatestStatus ? (
  <span className="shrink-0">
    <MessageStatusChip
      direction="outbound"
      delivery_status={latest.status}
      review_status={latest.review_status}
    />
  </span>
) : pending ? (
  <span
    className="shrink-0"
    style={{
      fontSize: 10,
      fontWeight: 600,
      color: "var(--warn-strong)",
      background: "var(--warn-bg)",
      borderRadius: 999,
      padding: "1px 8px",
      whiteSpace: "nowrap",
    }}
  >
    Pending
  </span>
) : null}
```

- [ ] **Step 5: Implement outbound status in `ThreadBubble.tsx`**

Add:

```tsx
import { MessageStatusChip } from "./MessageStatusChip";
```

Replace the pending-only chip beside `senderEmail` with:

```tsx
{!isInbound && (
  <MessageStatusChip
    direction="outbound"
    delivery_status={message.status}
    review_status={message.review_status}
  />
)}
```

Keep the `pending` boolean for the delete-button restriction. Do not change the
body-fetch or cache-invalidation logic.

- [ ] **Step 6: Run both component tests and verify they pass**

Run:

```bash
cd web
npm test -- --runInBand src/app/components/messages/ThreadRow.test.tsx src/app/components/messages/ThreadBubble.test.tsx
```

Expected: PASS.

- [ ] **Step 7: Commit the UI status placement**

```bash
git add web/src/app/components/messages/ThreadRow.tsx web/src/app/components/messages/ThreadRow.test.tsx web/src/app/components/messages/ThreadBubble.tsx web/src/app/components/messages/ThreadBubble.test.tsx
git commit -m "feat(web): show attention-aware delivery status"
```

## Task 3: Shared scoped polling configuration

**Files:**
- Create: `web/src/lib/livePolling.ts`
- Create: `web/src/lib/livePolling.test.ts`

- [ ] **Step 1: Write the polling-options contract test**

Create `livePolling.test.ts`:

```ts
import {
  inboxPolling,
  pendingPolling,
  unreadPolling,
} from "./livePolling";

describe("live dashboard polling", () => {
  it("refreshes inbox messages and review holds every 10 seconds", () => {
    expect(inboxPolling).toEqual({
      refreshInterval: 10_000,
      refreshWhenHidden: false,
      refreshWhenOffline: false,
    });
    expect(pendingPolling).toEqual(inboxPolling);
  });

  it("refreshes mounted unread probes every 15 seconds", () => {
    expect(unreadPolling).toEqual({
      refreshInterval: 15_000,
      refreshWhenHidden: false,
      refreshWhenOffline: false,
    });
  });
});
```

- [ ] **Step 2: Run the test and verify the module is missing**

Run:

```bash
cd web
npm test -- --runInBand src/lib/livePolling.test.ts
```

Expected: FAIL with `Cannot find module './livePolling'`.

- [ ] **Step 3: Implement the shared polling options**

Create `livePolling.ts`:

```ts
export const inboxPolling = {
  refreshInterval: 10_000,
  refreshWhenHidden: false,
  refreshWhenOffline: false,
} as const;

export const pendingPolling = inboxPolling;

export const unreadPolling = {
  refreshInterval: 15_000,
  refreshWhenHidden: false,
  refreshWhenOffline: false,
} as const;
```

- [ ] **Step 4: Run the test and verify it passes**

Run:

```bash
cd web
npm test -- --runInBand src/lib/livePolling.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit the polling contract**

```bash
git add web/src/lib/livePolling.ts web/src/lib/livePolling.test.ts
git commit -m "feat(web): define live polling cadence"
```

## Task 4: Poll the active inbox

**Files:**
- Modify: `web/src/app/(app)/inboxes/(view)/messages/page.test.tsx`
- Modify: `web/src/app/(app)/inboxes/(view)/messages/page.tsx`

- [ ] **Step 1: Add a timer-driven inbox refresh test**

Import `act` from the existing SWR test helper:

```tsx
import { render, screen, waitFor, within, act } from "../../../../../test-utils/swr";
```

Add to the `AgentInboxPage` suite:

```tsx
it("polls the active inbox every 10 seconds", async () => {
  setSearchParams({ email: "support@acme.io" });
  mockMessages([ORPHAN_INBOUND]);

  render(<AgentInboxPage />);
  await waitFor(() => expect(screen.getByTestId("thread-row")).toBeInTheDocument());
  const initialCalls = mockFetch.mock.calls.length;

  await act(async () => {
    jest.advanceTimersByTime(10_000);
    await Promise.resolve();
  });

  await waitFor(() => {
    expect(mockFetch.mock.calls.length).toBeGreaterThan(initialCalls);
  });
});
```

- [ ] **Step 2: Run the test and verify no interval refresh occurs**

Run:

```bash
cd web
npm test -- --runInBand 'src/app/(app)/inboxes/(view)/messages/page.test.tsx'
```

Expected: FAIL because `mockFetch` remains at the initial call count after 10 seconds.

- [ ] **Step 3: Apply `inboxPolling` to the inbox SWR query**

Import:

```tsx
import { inboxPolling } from "../../../../../lib/livePolling";
```

Change the existing per-query options to preserve the agent-switch safety rule
while enabling polling:

```tsx
{
  ...inboxPolling,
  keepPreviousData: false,
}
```

- [ ] **Step 4: Run the inbox test and verify it passes**

Run:

```bash
cd web
npm test -- --runInBand 'src/app/(app)/inboxes/(view)/messages/page.test.tsx'
```

Expected: PASS, including the new interval assertion and existing pagination,
selection, and pending-callout cases.

- [ ] **Step 5: Commit active-inbox polling**

```bash
git add 'web/src/app/(app)/inboxes/(view)/messages/page.tsx' 'web/src/app/(app)/inboxes/(view)/messages/page.test.tsx'
git commit -m "feat(web): poll active inbox messages"
```

## Task 5: Poll Pending Review and unread badges

**Files:**
- Modify: `web/src/app/components/hooks/usePendingCount.test.tsx`
- Create: `web/src/app/components/hooks/usePendingCount.polling.test.tsx`
- Modify: `web/src/app/components/hooks/usePendingCount.ts`
- Modify: `web/src/app/(app)/reviews/page.test.tsx`
- Modify: `web/src/app/(app)/reviews/page.tsx`
- Modify: `web/src/app/(app)/inboxes/_components/AgentCard.test.tsx`
- Modify: `web/src/app/(app)/inboxes/_components/AgentCard.tsx`

- [ ] **Step 1: Add source-level polling assertions to the focused tests**

In `usePendingCount.test.tsx`, mock `swr` before importing the hook and assert
that the shared key receives `pendingPolling`. Because this file currently
tests real SWR behavior, place the option assertion in a new sibling file
`usePendingCount.polling.test.tsx` to avoid invalidating its existing tests:

```tsx
import useSWR from "swr";
import { usePendingCount } from "./usePendingCount";
import { pendingMessagesKey } from "../../../lib/swrKeys";
import { pendingPolling } from "../../../lib/livePolling";

jest.mock("swr", () => ({ __esModule: true, default: jest.fn() }));
jest.mock("../onboarding/api", () => ({ listPendingMessages: jest.fn() }));

const mockUseSWR = useSWR as jest.Mock;

it("subscribes the shared pending key with live polling", () => {
  mockUseSWR.mockReturnValue({ data: [], error: undefined });
  usePendingCount();
  expect(mockUseSWR).toHaveBeenCalledWith(
    pendingMessagesKey,
    expect.any(Function),
    pendingPolling,
  );
});
```

Create the file at
`web/src/app/components/hooks/usePendingCount.polling.test.tsx` and add it to
the task's file list when committing.

In `AgentCard.test.tsx`, add a behavioral fake-timer case using the existing
mocked `getInboxUnread`:

```tsx
it("refreshes the unread probe every 15 seconds", async () => {
  jest.useFakeTimers();
  mockUnread.mockResolvedValue({ count: 1, more: false });
  render(<AgentCard agent={agent} />);
  await waitFor(() => expect(screen.getByTitle("1 unread")).toBeInTheDocument());
  const initialCalls = mockUnread.mock.calls.length;

  await act(async () => {
    jest.advanceTimersByTime(15_000);
    await Promise.resolve();
  });
  await waitFor(() => expect(mockUnread.mock.calls.length).toBeGreaterThan(initialCalls));
  jest.useRealTimers();
});
```

Add `act` to the test-helper import and make `afterEach` always call
`jest.useRealTimers()` before resetting the mock.

The existing Review-page cache-sharing test already proves the page and sidebar
use `pendingMessagesKey`; no second timer test is needed for the same cache.

- [ ] **Step 2: Run the focused tests and verify they fail**

Run:

```bash
cd web
npm test -- --runInBand src/app/components/hooks/usePendingCount.polling.test.tsx 'src/app/(app)/reviews/page.test.tsx' 'src/app/(app)/inboxes/_components/AgentCard.test.tsx'
```

Expected: FAIL because the hook has no third SWR argument and AgentCard does not
refresh after 15 seconds.

- [ ] **Step 3: Apply pending polling to both consumers of the shared key**

In `usePendingCount.ts`, import `pendingPolling` and change the hook to:

```tsx
const { data, error } = useSWR(
  pendingMessagesKey,
  () => listPendingMessages(),
  pendingPolling,
);
```

In `reviews/page.tsx`, import `pendingPolling` and change the existing call to:

```tsx
useSWR<PendingMessageSummary[]>(
  pendingMessagesKey,
  () => listPendingMessages(),
  pendingPolling,
);
```

Passing identical key, fetcher behavior, and options preserves SWR sharing and
deduplication between the sidebar and Review page.

- [ ] **Step 4: Apply unread polling to `AgentCard`**

Import `unreadPolling` and pass it as the third SWR argument:

```tsx
const { data: unread } = useSWR(
  agentUnreadKey(agent.email),
  () => getInboxUnread(agent.email).catch(() => ({ count: 0, more: false })),
  unreadPolling,
);
```

- [ ] **Step 5: Run the focused tests and verify they pass**

Run:

```bash
cd web
npm test -- --runInBand src/app/components/hooks/usePendingCount.test.tsx src/app/components/hooks/usePendingCount.polling.test.tsx 'src/app/(app)/reviews/page.test.tsx' 'src/app/(app)/inboxes/_components/AgentCard.test.tsx'
```

Expected: PASS.

- [ ] **Step 6: Commit shared Pending and unread polling**

```bash
git add web/src/app/components/hooks/usePendingCount.ts web/src/app/components/hooks/usePendingCount.test.tsx web/src/app/components/hooks/usePendingCount.polling.test.tsx 'web/src/app/(app)/reviews/page.tsx' 'web/src/app/(app)/reviews/page.test.tsx' 'web/src/app/(app)/inboxes/_components/AgentCard.tsx' 'web/src/app/(app)/inboxes/_components/AgentCard.test.tsx'
git commit -m "feat(web): refresh pending and unread counts"
```

## Task 6: Full verification and documentation consistency

**Files:**
- Modify only if a verification failure reveals a scoped defect.

- [ ] **Step 1: Run all focused feature tests together**

```bash
cd web
npm test -- --runInBand \
  src/lib/livePolling.test.ts \
  src/app/components/messages/MessageStatusChip.test.tsx \
  src/app/components/messages/ThreadRow.test.tsx \
  src/app/components/messages/ThreadBubble.test.tsx \
  'src/app/(app)/inboxes/(view)/messages/page.test.tsx' \
  src/app/components/hooks/usePendingCount.test.tsx \
  src/app/components/hooks/usePendingCount.polling.test.tsx \
  'src/app/(app)/reviews/page.test.tsx' \
  'src/app/(app)/inboxes/_components/AgentCard.test.tsx'
```

Expected: PASS with no open handles or timer warnings.

- [ ] **Step 2: Run the complete web test suite**

```bash
cd web
npm test -- --runInBand
```

Expected: PASS.

- [ ] **Step 3: Run lint**

```bash
cd web
npm run lint
```

Expected: exit 0 with no ESLint errors.

- [ ] **Step 4: Run the production build**

```bash
cd web
npm run build
```

Expected: Next.js static export completes successfully. The prebuild agent-doc
sync must not produce an unrelated diff.

- [ ] **Step 5: Check the final diff for generated or unrelated changes**

```bash
git status --short
git diff --check
git diff --stat HEAD~4..HEAD
```

Expected: only the web status/polling files, tests, and the approved design/plan
documents are changed. `.superpowers/` remains untracked and must not be staged.

- [ ] **Step 6: Commit any verification-only correction**

Only if Steps 1–4 required a source or test correction:

```bash
git add \
  web/src/lib/livePolling.ts \
  web/src/lib/livePolling.test.ts \
  web/src/app/components/messages/MessageStatusChip.tsx \
  web/src/app/components/messages/MessageStatusChip.test.tsx \
  web/src/app/components/messages/ThreadRow.tsx \
  web/src/app/components/messages/ThreadRow.test.tsx \
  web/src/app/components/messages/ThreadBubble.tsx \
  web/src/app/components/messages/ThreadBubble.test.tsx \
  'web/src/app/(app)/inboxes/(view)/messages/page.tsx' \
  'web/src/app/(app)/inboxes/(view)/messages/page.test.tsx' \
  web/src/app/components/hooks/usePendingCount.ts \
  web/src/app/components/hooks/usePendingCount.test.tsx \
  web/src/app/components/hooks/usePendingCount.polling.test.tsx \
  'web/src/app/(app)/reviews/page.tsx' \
  'web/src/app/(app)/reviews/page.test.tsx' \
  'web/src/app/(app)/inboxes/_components/AgentCard.tsx' \
  'web/src/app/(app)/inboxes/_components/AgentCard.test.tsx'
git commit -m "test(web): harden live inbox refresh"
```

If no correction was needed, do not create an empty commit.
