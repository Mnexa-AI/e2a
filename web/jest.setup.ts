import "@testing-library/jest-dom";
import { mutate } from "swr";

// SWR uses a module-level cache that persists across tests in the
// same file. Nuke it after each test so components mounted in the
// next test start in the "no data yet" state — otherwise a fetcher
// from test A can leak data into test B's first render.
//
// This catches two cases the test-utils/swr render-wrapper can't:
//   1. Hook tests that call @testing-library/react's `renderHook`
//      directly without the WithSWR wrapper (e.g.
//      usePendingCount.test.tsx).
//   2. Any future test that imports `render` from the raw RTL
//      package instead of test-utils/swr.
// So this isn't redundant with the wrapper — it's the safety net
// for code paths that go around it.
afterEach(async () => {
  await mutate(() => true, undefined, { revalidate: false });
});
