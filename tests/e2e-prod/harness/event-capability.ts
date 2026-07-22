export function isEventsLogDisabled(status: number, body: unknown): boolean {
  if (status !== 501 || typeof body !== "object" || body === null) return false;
  const error = (body as { error?: unknown }).error;
  if (typeof error !== "object" || error === null) return false;
  return (error as { code?: unknown }).code === "events_log_disabled";
}
