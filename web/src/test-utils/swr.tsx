// Test helper for SWR-using components. Re-exports everything from
// @testing-library/react with a `render` override that wraps every
// rendered tree in a fresh-cache <SWRConfig> provider. Tests just
// import from this module instead of @testing-library/react and
// don't have to pass `{ wrapper: ... }` on every render call.
//
// Usage in a test file:
//
//   import { render, screen, waitFor } from "../../test-utils/swr";
//   render(<MyPage />);
//
// Tests that don't touch SWR-backed data can keep importing from
// @testing-library/react directly — the wrap is a no-op for them.

import type { ReactNode, ReactElement } from "react";
import { SWRConfig } from "swr";
import {
  render as baseRender,
  type RenderOptions,
  type RenderResult,
} from "@testing-library/react";

function WithSWR({ children }: { children: ReactNode }) {
  return (
    <SWRConfig
      value={{
        // Each render gets a fresh Map cache — no data leaks across
        // tests that share the module-level default cache.
        provider: () => new Map(),
        // Disable revalidation triggers that fire in jsdom without
        // adding test value (focus events from RTL cleanup,
        // navigator.onLine flips during test setup).
        revalidateOnFocus: false,
        revalidateOnReconnect: false,
        // Drop dedup so consecutive renders inside a single test
        // (e.g. with rerender) fetch fresh.
        dedupingInterval: 0,
      }}
    >
      {children}
    </SWRConfig>
  );
}

export function render(ui: ReactElement, options?: RenderOptions): RenderResult {
  const UserWrapper = options?.wrapper;
  // Compose user-provided wrapper inside the SWR wrap so tests can
  // still pass their own context providers via the wrapper option.
  // The capitalization (UserWrapper, not userWrapper) matters —
  // JSX treats lowercase tags as DOM elements rather than variable
  // references.
  const Wrapper: React.FC<{ children?: ReactNode }> = ({ children }) =>
    UserWrapper ? (
      <WithSWR>
        <UserWrapper>{children}</UserWrapper>
      </WithSWR>
    ) : (
      <WithSWR>{children}</WithSWR>
    );
  return baseRender(ui, { ...options, wrapper: Wrapper });
}

// Re-export everything else so consumers can do a single import.
export {
  screen,
  waitFor,
  fireEvent,
  within,
  cleanup,
  act,
  renderHook,
} from "@testing-library/react";
