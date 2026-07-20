import { inboxPolling, pendingPolling, unreadPolling } from './livePolling';

describe('live polling configuration', () => {
  it('polls inbox data every 10 seconds only while visible and online', () => {
    expect(inboxPolling).toEqual({
      refreshInterval: 10000,
      refreshWhenHidden: false,
      refreshWhenOffline: false,
    });
  });

  it('uses the inbox polling contract for pending data', () => {
    expect(pendingPolling).toEqual({
      refreshInterval: 10000,
      refreshWhenHidden: false,
      refreshWhenOffline: false,
    });
  });

  it('polls unread data every 15 seconds only while visible and online', () => {
    expect(unreadPolling).toEqual({
      refreshInterval: 15000,
      refreshWhenHidden: false,
      refreshWhenOffline: false,
    });
  });
});
