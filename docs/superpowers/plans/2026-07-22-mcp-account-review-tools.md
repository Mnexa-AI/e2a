# MCP Account Review Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make MCP review discovery account-only, complete for both directions, and backed directly by the canonical paginated reviews SDK resource.

**Architecture:** Move `list_reviews` and `get_review` into the admin tool tier. Replace the per-agent outbound message scan in `McpClient` with direct `sdk.reviews.list().page()` and `sdk.reviews.get()` delegation, then expose the existing MCP pagination envelope at the tool boundary.

**Tech Stack:** TypeScript, MCP TypeScript SDK, `@e2a/sdk`, Zod, Vitest.

---

## File structure

- Modify `mcp/src/client.ts`: direct typed review-resource delegation.
- Modify `mcp/src/tools/review.ts`: account-only descriptions, pagination schema, and response envelope.
- Modify `mcp/src/tools/tiers.ts`: move review discovery from runtime to admin.
- Modify `mcp/tests/client.test.ts`: lock direct SDK routing and prevent message/agent fan-out.
- Modify `mcp/tests/tools.test.ts`: lock catalog scope, pagination inputs, and MCP output shape.

### Task 1: Lock canonical review SDK routing

**Files:**
- Modify: `mcp/tests/client.test.ts`
- Modify: `mcp/src/client.ts`

- [ ] **Step 1: Write failing client-routing tests**

Replace the review mock with a canonical pager:

```ts
const reviewPage = vi.fn(async (cursor?: string) => ({
  items: [
    { id: "msg_in", direction: "inbound" },
    { id: "msg_out", direction: "outbound" },
  ],
  next_cursor: cursor ? undefined : "reviews_next",
}));

reviews: {
  list: vi.fn(() => ({ page: reviewPage })),
  get: vi.fn(async (id: string) => ({ id })),
  approve: vi.fn(async () => ({ messageId: "msg_p", status: "sent" })),
  reject: vi.fn(async () => ({ messageId: "msg_p", status: "rejected" })),
}
```

Replace the agent-visible routing assertions with tests that:

```ts
const page = await c.listReviews({ cursor: "reviews_cursor", limit: 25 });
expect(sdk.reviews.list).toHaveBeenCalledWith({ limit: 25 });
expect(reviewPage).toHaveBeenCalledWith("reviews_cursor");
expect(page.items.map((row) => row.direction)).toEqual(["inbound", "outbound"]);
expect(sdk.agents.list).not.toHaveBeenCalled();
expect(sdk.messages.list).not.toHaveBeenCalled();

await c.getReview("msg_p");
expect(sdk.reviews.get).toHaveBeenCalledWith("msg_p");
expect(sdk.messages.get).not.toHaveBeenCalled();
```

- [ ] **Step 2: Run the focused client tests and verify RED**

Run:

```bash
npm exec --workspace @e2a/mcp-server -- vitest run tests/client.test.ts
```

Expected: FAIL because `listReviews` has no pagination arguments and both methods still scan `sdk.messages`.

- [ ] **Step 3: Implement direct review-resource delegation**

Import `ReviewView`, remove `MessageSummaryView`, `PENDING_REVIEW_STATUS`, and the now-unused `listAllAgents` helper. Define:

```ts
listReviews(params: { cursor?: string; limit?: number } = {}): Promise<Page<ReviewView>> {
  const { cursor, ...rest } = params;
  return this.sdk.reviews.list(rest).page(cursor);
}

getReview(messageId: string): Promise<MessageView> {
  return this.sdk.reviews.get(messageId);
}
```

Delete `ownerOfPending` and its synthetic `CodedError` path. Remove the `CodedError` import if no other caller remains.

- [ ] **Step 4: Run the focused client tests and verify GREEN**

Run the command from Step 2. Expected: all client tests PASS.

### Task 2: Enforce the account-only paginated tool contract

**Files:**
- Modify: `mcp/tests/tools.test.ts`
- Modify: `mcp/src/tools/review.ts`
- Modify: `mcp/src/tools/tiers.ts`

- [ ] **Step 1: Write failing tool and scope tests**

Update the scope assertions so an agent-scoped catalog excludes
`list_reviews`, `get_review`, `approve_review`, and `reject_review`, while an
account-scoped catalog includes all four.

Update the `list_reviews` tool test to call:

```ts
const result = await client.callTool({
  name: "list_reviews",
  arguments: { cursor: "reviews_cursor", limit: 25 },
});
expect(stub.listReviews).toHaveBeenCalledWith({
  cursor: "reviews_cursor",
  limit: 25,
});
expect(JSON.parse(text(result))).toEqual({
  reviews: expect.any(Array),
  next_cursor: "reviews_next",
});
```

Add a last-page assertion that `next_cursor` is omitted. Ensure the stub page
contains one inbound and one outbound review row.

- [ ] **Step 2: Run focused tool tests and verify RED**

Run:

```bash
npm exec --workspace @e2a/mcp-server -- vitest run tests/tools.test.ts
```

Expected: FAIL because the tools remain runtime-tier, reject pagination inputs,
and return a flat array from the old client contract.

- [ ] **Step 3: Implement the tool contract**

Move `list_reviews` and `get_review` from `RUNTIME_TOOLS` to `ADMIN_TOOLS`.
Import `paginationInput` in `review.ts`, spread it into `list_reviews`' strict
input schema, and call `client.listReviews` with defined cursor/limit fields.
Return:

```ts
{
  reviews: page.items,
  ...(page.next_cursor ? { next_cursor: page.next_cursor } : {}),
}
```

Update both descriptions to state `Account scope only`, cover both inbound and
outbound holds, document cursor use, and state that review IDs are shared by
get/approve/reject.

- [ ] **Step 4: Run focused MCP tests and verify GREEN**

Run both focused test files. Expected: PASS.

### Task 3: Verify, review, and publish

**Files:**
- Verify all files changed above plus this plan and the approved design.

- [ ] **Step 1: Run full verification**

```bash
npm run build --workspace @e2a/sdk
npm run build --workspace @e2a/mcp-server
npm test --workspace @e2a/mcp-server
git diff --check origin/main...HEAD
git status --short
```

Expected: builds, all MCP tests, and type tests PASS; diff check is clean.

- [ ] **Step 2: Review against the approved design**

Confirm no review-discovery path calls `sdk.agents` or `sdk.messages`, all four
review tools are admin-tier, approve/reject behavior is unchanged, and no alias
work leaked into this PR.

- [ ] **Step 3: Commit and publish**

Commit the implementation and tests as:

```bash
git commit -m "fix(mcp): unify account review tools"
```

Push `codex/mcp-account-reviews` and open a draft PR against `main` containing
the contract correction and verification evidence. Do not merge it.
