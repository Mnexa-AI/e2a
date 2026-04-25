"use client";

import { useState, useCallback } from "react";
import { getAgentActivity } from "../../../components/onboarding/api";
import type { ActivityEntry } from "../../../components/types";

function ActivityDetail({ entry }: { entry: ActivityEntry }) {
  const [open, setOpen] = useState(false);

  return (
    <div>
      <button
        onClick={() => setOpen(!open)}
        className="text-[10px] text-muted hover:text-foreground transition"
      >
        {open ? "Hide details" : "Details"}
      </button>
      {open && (
        <div className="mt-1 text-[10px] text-muted space-y-0.5 pl-1 border-l-2 border-border">
          {entry.sender && (
            <p><span className="font-medium">From:</span> {entry.sender}</p>
          )}
          {entry.direction === "outbound" ? (
            <>
              {entry.to_recipients && entry.to_recipients.length > 0 && (
                <p><span className="font-medium">To:</span> {entry.to_recipients.join(", ")}</p>
              )}
              {entry.cc && entry.cc.length > 0 && (
                <p><span className="font-medium">CC:</span> {entry.cc.join(", ")}</p>
              )}
              {entry.bcc && entry.bcc.length > 0 && (
                <p><span className="font-medium">BCC:</span> {entry.bcc.join(", ")}</p>
              )}
            </>
          ) : (
            <p><span className="font-medium">To:</span> {entry.recipient}</p>
          )}
          {entry.subject && (
            <p><span className="font-medium">Subject:</span> {entry.subject}</p>
          )}
        </div>
      )}
    </div>
  );
}

export function ActivityPanel({ email }: { email: string }) {
  const [activity, setActivity] = useState<ActivityEntry[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [expanded, setExpanded] = useState(false);

  const fetchActivity = useCallback(async () => {
    setLoading(true);
    try {
      const data = await getAgentActivity(email);
      setActivity(data);
    } catch {
      // Non-fatal
    } finally {
      setLoading(false);
    }
  }, [email]);

  const toggle = () => {
    const next = !expanded;
    setExpanded(next);
    if (next) fetchActivity();
  };

  return (
    <div>
      <button
        onClick={toggle}
        className="text-xs text-muted hover:text-foreground transition flex items-center gap-1"
      >
        {expanded ? "Hide" : "Show"} Activity
        <span className="text-[10px]">{expanded ? "\u25B2" : "\u25BC"}</span>
      </button>

      {expanded && (
        <div className="mt-3 border-t border-border pt-3">
          {loading ? (
            <p className="text-xs text-muted">Loading...</p>
          ) : activity && activity.length === 0 ? (
            <p className="text-xs text-muted">No recent activity</p>
          ) : (
            <div className="space-y-2 max-h-64 overflow-y-auto">
              {activity?.map((entry) => (
                <div key={entry.id} className="flex items-start gap-2 text-xs">
                  <span
                    className={`shrink-0 inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${
                      entry.direction === "inbound"
                        ? "bg-blue-100 text-blue-700"
                        : "bg-green-100 text-green-700"
                    }`}
                  >
                    {entry.direction === "inbound" ? "IN" : "OUT"}
                  </span>
                  <div className="min-w-0 flex-1">
                    <p className="truncate">
                      <span className="text-muted">
                        {entry.direction === "inbound" ? "From" : "To"}:
                      </span>{" "}
                      {entry.direction === "inbound"
                        ? entry.sender
                        : (entry.to_recipients?.join(", ") || entry.recipient)}
                    </p>
                    {entry.direction === "outbound" && entry.cc && entry.cc.length > 0 && (
                      <p className="truncate text-muted text-[10px]">
                        CC: {entry.cc.join(", ")}
                      </p>
                    )}
                    {entry.subject && (
                      <p className="truncate text-muted">{entry.subject}</p>
                    )}
                    {entry.webhook_error && (
                      <p className="truncate text-red-600 text-[10px] mt-0.5">
                        Error: {entry.webhook_error}
                      </p>
                    )}
                    <ActivityDetail entry={entry} />
                  </div>
                  <div className="ml-auto shrink-0 text-muted flex items-center gap-1.5">
                    {entry.webhook_status === "delivered" && (
                      <span className="text-[10px] text-green-600">&#10003;</span>
                    )}
                    {entry.webhook_status === "pending" && (
                      <span className="text-[10px] px-1 py-0.5 rounded bg-amber-100 text-amber-700">
                        retrying{entry.webhook_attempts ? ` (${entry.webhook_attempts})` : ""}
                      </span>
                    )}
                    {entry.webhook_status === "failed" && (
                      <span
                        className="text-[10px] px-1 py-0.5 rounded bg-red-100 text-red-700 cursor-help"
                        title={entry.webhook_error || "delivery failed"}
                      >
                        failed
                      </span>
                    )}
                    {entry.method && (
                      <span className="text-[10px] uppercase opacity-60">{entry.method}</span>
                    )}
                    {new Date(entry.created_at).toLocaleString(undefined, { month: "numeric", day: "numeric", year: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit" })}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}
