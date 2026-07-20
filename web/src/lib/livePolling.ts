export const inboxPolling = {
  refreshInterval: 10000,
  refreshWhenHidden: false,
  refreshWhenOffline: false,
} as const;

export const pendingPolling = inboxPolling;

export const unreadPolling = {
  refreshInterval: 15000,
  refreshWhenHidden: false,
  refreshWhenOffline: false,
} as const;
