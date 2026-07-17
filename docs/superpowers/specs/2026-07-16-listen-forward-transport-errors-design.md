# Listen Forward Transport Error Design

## Problem statement

PR #497 made `--forward` independent from JSON selection, but a rejected
network request still escapes `forwardMessage`. Because callers await the
forward before rendering, a transport error suppresses requested output and,
under `--once`, prevents the matching message from completing the wait.

## Goals and non-goals

- Preserve JSON/text/TSV rendering and `--once` completion when the forwarding
  endpoint cannot be reached.
- Report the forwarding failure on stderr, consistent with non-2xx responses.
- Preserve successful generic and OpenClaw forwarding behavior.
- Do not add retries, change exit codes, or alter message-fetch failures.

## Proposed design

Handle only `fetch` transport rejections inside `forwardMessage`. Convert the
unknown rejection to a readable message, write `Forward failed: <message>` to
stderr, and return. Keep message retrieval outside the catch because rendering
also depends on retrieval and SDK errors should retain their existing mapping.

This shared boundary covers continuous listening and `--once` without adding
duplicated caller-level catches. It also matches the existing treatment of
non-2xx forward responses, which are reported and returned rather than thrown.

## Failure handling

- Rejected `fetch`: log and return; callers continue rendering/completing.
- Non-2xx response: retain the existing status/body diagnostic.
- Message retrieval failure: continue throwing because no payload is available.
- Successful OpenClaw response: retain response parsing and auto-reply behavior.

## Verification strategy

- Add a `handleNotification` regression test proving rejected forwarding still
  emits JSON and reports the error.
- Add a `listen({ once: true, json: true, forward: ... })` regression test proving
  the first matching message renders once and closes the stream after rejection.
- Run the focused listen tests, the full CLI test suite, and the CLI build.

## Open questions

None. Retry and exit-code policy remain intentionally out of scope.
