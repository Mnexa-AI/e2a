// Trash retention helpers shared by the account-wide /trash page and each
// inbox's Trash tab.
//
// TRASH_RETENTION_DAYS mirrors the backend window (identity.TrashRetention,
// default 30 days). Display-only — the janitor owns the real clock; if the
// backend window is ever tuned, update this constant (or, better, switch the
// API to emit a server-computed purge_at and delete this file).
export const TRASH_RETENTION_DAYS = 30;

// daysLeft returns the whole days remaining until a trashed resource is
// purged, given its deleted_at timestamp. Clamped at 0.
export function daysLeft(deletedAt: string): number {
  const purgeAt =
    new Date(deletedAt).getTime() + TRASH_RETENTION_DAYS * 24 * 3600 * 1000;
  return Math.max(0, Math.ceil((purgeAt - Date.now()) / (24 * 3600 * 1000)));
}
