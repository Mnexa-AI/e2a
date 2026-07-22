"use client";

import useSWR from "swr";
import { unreadPolling } from "../../../lib/livePolling";
import { accountUnreadKey } from "../../../lib/swrKeys";
import {
  getInboxUnread,
  listAgents,
  UNREAD_BADGE_CAP,
} from "../onboarding/api";

export type UnreadCount = { count: number; more: boolean };

export async function loadUnreadCount(): Promise<UnreadCount> {
  const agents = await listAgents();
  const unread = await Promise.all(
    agents.map(({ email }) => getInboxUnread(email)),
  );
  const total = unread.reduce((sum, result) => sum + result.count, 0);

  return {
    count: Math.min(total, UNREAD_BADGE_CAP),
    more: total > UNREAD_BADGE_CAP || unread.some((result) => result.more),
  };
}

export function useUnreadCount(): UnreadCount | null {
  const { data } = useSWR(accountUnreadKey, loadUnreadCount, unreadPolling);
  return data ?? null;
}
