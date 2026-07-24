export type LogSeverity = "INFO" | "WARNING" | "ERROR";

/**
 * Request-scoped structured log sink: one operational event as a single-line
 * JSON object. Same shape as the bin's logJson (severity / event / message +
 * fields) so GCE Cloud Logging parses it into structured jsonPayload fields.
 */
export type Logger = (
  severity: LogSeverity,
  event: string,
  message: string,
  fields?: Record<string, unknown>,
) => void;

/**
 * Default sink: single-line JSON on stderr. Keeps the library usable
 * standalone (tests, embedding); the bin passes its own logJson instead so
 * all process logs share one writer.
 */
export const defaultLogger: Logger = (severity, event, message, fields = {}) => {
  process.stderr.write(`${JSON.stringify({ severity, event, message, ...fields })}\n`);
};
