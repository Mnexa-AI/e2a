export type ToolResult = {
  content: Array<{ type: "text"; text: string }>;
  isError?: boolean;
};

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
