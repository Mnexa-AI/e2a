import "@testing-library/jest-dom";
import { mutate } from "swr";

// SWR uses a module-level cache that persists across tests in the
// same file. Nuke it after each test so components mounted in the
// next test start in the "no data yet" state — otherwise a fetcher
// from test A can leak data into test B's first render.
afterEach(async () => {
  await mutate(() => true, undefined, { revalidate: false });
});
