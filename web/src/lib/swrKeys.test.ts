// Invalidation helpers are silent-failure-prone: a predicate that stops
// matching doesn't throw, it just leaves stale data cached with no visible
// error. `invalidateMessageDetail` is the sharpest case — it's what makes
// an approved/rejected message's detail refetch, and its predicate matches
// on a hand-written key SHAPE rather than on `messageDetailKey` itself. The
// per-message key was recently re-shaped from ["pending-message", email,
// id] to ["message-detail", id] (collapsing two colliding per-surface
// entries into one); a predicate left on the old shape would match nothing
// and approve/reject would leave the focus page showing a stale
// "Pending review" forever.
//
// These run against the MODULE-LEVEL SWR cache (no test-utils/swr
// fresh-Map wrapper) because the exported helpers call SWR's global
// `mutate`, which is bound to that cache. jest.setup.ts clears it between
// tests.

import { renderHook, waitFor, act } from "@testing-library/react";
import useSWR from "swr";
import { invalidateMessageDetail, messageDetailKey } from "./swrKeys";

describe("messageDetailKey", () => {
  it("keys a message by id ALONE — no owning-inbox component", () => {
    // Message ids are globally unique, so the id is the identity. The
    // owning agent's email is a fetch parameter only. Putting the email in
    // the key would re-split the entry the review queue and the mail
    // surfaces are meant to share (they reach the same message through
    // different endpoints and only one of them knows an inbox address).
    expect(messageDetailKey("msg_abc")).toEqual(["message-detail", "msg_abc"]);
  });
});

describe("invalidateMessageDetail", () => {
  it("revalidates the entry written under the real messageDetailKey", async () => {
    // Wired through useSWR with the real key helper rather than poking the
    // cache directly, so the predicate is checked against exactly the key
    // shape a live subscriber holds.
    const fetcher = jest.fn().mockResolvedValue({ id: "msg_a" });
    renderHook(() => useSWR(messageDetailKey("msg_a"), fetcher));
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(1));

    await act(async () => {
      await invalidateMessageDetail("msg_a");
    });

    // A second fetch means the entry was dropped and refetched. With a
    // predicate that no longer matches this key shape, the count stays 1
    // and the surface keeps rendering pre-approval data.
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
  });

  it("leaves other messages' entries alone", async () => {
    // The predicate scopes to one id: approving message A must not force
    // every other open message detail in the dashboard to refetch.
    // Distinct ids per test: SWR's request-dedup window is keyed globally
    // and outlives the cache reset in jest.setup.ts, so reusing an id from
    // the test above would suppress the first fetch here.
    const fetcherA = jest.fn().mockResolvedValue({ id: "msg_scoped_a" });
    const fetcherB = jest.fn().mockResolvedValue({ id: "msg_scoped_b" });
    renderHook(() => useSWR(messageDetailKey("msg_scoped_a"), fetcherA));
    renderHook(() => useSWR(messageDetailKey("msg_scoped_b"), fetcherB));
    await waitFor(() => {
      expect(fetcherA).toHaveBeenCalledTimes(1);
      expect(fetcherB).toHaveBeenCalledTimes(1);
    });

    await act(async () => {
      await invalidateMessageDetail("msg_scoped_a");
    });

    await waitFor(() => expect(fetcherA).toHaveBeenCalledTimes(2));
    expect(fetcherB).toHaveBeenCalledTimes(1);
  });
});
