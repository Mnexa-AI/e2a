import { z, type ZodRawShape } from "zod";

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
    const message = err instanceof Error ? err.message : String(err);
    return {
      content: [{ type: "text", text: `e2a error: ${message}` }],
      isError: true,
    };
  }
}
