// Runs before any modules are imported (via jest.config setupFiles).
// Tests assume the deployment exposes a shared agent domain — set the
// public env var here so lib/site picks it up at module evaluation time.
process.env.NEXT_PUBLIC_AGENTS_DOMAIN = "agents.e2a.dev";

// jsdom in this Jest version doesn't expose the WHATWG Text* APIs that
// real browsers + modern Node provide. Polyfill from `util` so message
// body decoding (the focus page's UTF-8 atob → bytes → TextDecoder
// path) works in tests the same way it does at runtime.
import { TextDecoder, TextEncoder } from "util";
if (typeof globalThis.TextDecoder === "undefined") {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  globalThis.TextDecoder = TextDecoder as any;
}
if (typeof globalThis.TextEncoder === "undefined") {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  globalThis.TextEncoder = TextEncoder as any;
}
