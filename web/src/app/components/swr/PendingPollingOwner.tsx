"use client";

import useSWR from "swr";
import { pendingPolling } from "../../../lib/livePolling";
import { pendingMessagesKey } from "../../../lib/swrKeys";
import { listPendingMessages } from "../onboarding/api";

export function PendingPollingOwner() {
  useSWR(pendingMessagesKey, listPendingMessages, pendingPolling);
  return null;
}
