import { z, type ZodRawShape } from "zod";
import { E2AError } from "@e2a/sdk/v1";

export type ToolResult = {
  content: Array<{ type: "text"; text: string }>;
  isError?: boolean;
};

// strictInputSchema wraps a zod raw shape in a strict ZodObject so the
// MCP SDK rejects unknown argument keys instead of silently stripping
// them. Without this, a typo like `limit` against a tool that takes
// `page_size` succeeds with the bogus arg ignored — which the e2e
// contract sweep caught as a foot-gun. Apply to every registerTool call.
export function strictInputSchema<S extends ZodRawShape>(shape: S) {
  return z.object(shape).strict();
}

export async function runTool<T>(fn: () => Promise<T>): Promise<ToolResult> {
  try {
    const result = await fn();
    const text =
      result === undefined
        ? "OK"
        : typeof result === "string"
          ? result
          : JSON.stringify(result, null, 2);
    return { content: [{ type: "text", text }] };
  } catch (err) {
    // Surface the API envelope's machine-branchable `code` (§6a #4) so an agent
    // can branch on a stable code (e.g. domain_not_verified, message_not_pending,
    // recipient_suppressed) instead of parsing prose. The retryable hint tells
    // it whether a retry could ever help. Errors thrown by the wrapper itself
    // (plain Error — e.g. "email is required") have no code and fall through to
    // the prose form.
    if (err instanceof E2AError && err.code) {
      const retry = err.retryable ? " (retryable)" : "";
      // Only the `code` (a trusted snake_case token) goes inside the brackets.
      // The message is free-form and can echo caller/recipient input, so bound +
      // sanitize it: strip control chars/newlines (keep the `[code]` convention
      // parseable) and cap length (avoid context blowup). We surface code +
      // message only — never the raw response body, headers, or request_id.
      return {
        content: [{ type: "text", text: `e2a error [${err.code}]${retry}: ${sanitizeMessage(err.message)}` }],
        isError: true,
      };
    }
    const message = err instanceof Error ? err.message : String(err);
    return {
      content: [{ type: "text", text: `e2a error: ${sanitizeMessage(message)}` }],
      isError: true,
    };
  }
}

// sanitizeMessage makes an error message safe to splice into the single-line
// `[code]: <message>` tool-error text: collapse control chars / newlines to
// spaces (so they can't forge a second `[code]` bracket or break a parser) and
// cap the length (an attacker-influenced or raw message must not blow up the
// agent's context).
const MAX_ERROR_MESSAGE_LEN = 500;
function sanitizeMessage(message: string): string {
  // eslint-disable-next-line no-control-regex
  const oneLine = message.replace(/[\x00-\x1f\x7f]+/g, " ").trim();
  return oneLine.length > MAX_ERROR_MESSAGE_LEN
    ? oneLine.slice(0, MAX_ERROR_MESSAGE_LEN) + "…"
    : oneLine;
}
