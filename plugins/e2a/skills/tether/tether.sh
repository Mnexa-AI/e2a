#!/usr/bin/env bash
# tether.sh — the runtime CLI the agent calls to stay in touch over email.
#
#   tether.sh start <your-email>   send the intro email, open the thread, arm
#   tether.sh update "<message>"   send a threaded update ("as you see fit")
#   tether.sh ask "<question>"     email a question and BLOCK until the reply
#   tether.sh poll                 print any new replies since last poll (exit 0)
#   tether.sh status               show tether state
#   tether.sh stop                 disarm and clear state
#   tether.sh _selftest            unconfigured dry-run + syntax check
#
# Sending is agent-driven: call `update` when there's something worth reporting.
# Receiving is poll-driven: call `poll` on an interval (keep the session alive
# with /loop) so replies sent while idle are still picked up.
set -uo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "${here}/lib.sh"

cmd="${1:-}"; shift || true

need_config() { t_load_config || { echo "tether: not configured (need E2A_API_KEY + E2A_AGENT_EMAIL; see tether.env.example)"; exit 1; }; }
need_armed()  { [ "$(t_state_get armed)" = "1" ] || { echo "tether: not started — run 'tether.sh start <email>' first"; exit 1; }; }

case "$cmd" in
  start)
    need_config
    to="${1:-}"; [ -n "$to" ] || { echo "usage: tether.sh start <your-email>"; exit 2; }
    proj="$(basename "$PWD")"
    conv="tether-$(date +%s)-$$"
    intro="🪢 Tethered — ${proj}

This session is now tethered. I'll send updates to this thread as I make
meaningful progress, and I'll pick up your replies (usually within a few
minutes). Reply any time with a question or instruction; reply \"stop\" to end.

— your coding agent"
    mid="$(t_api_send "$to" "Tether: ${proj}" "$intro" "$conv")"
    [ -n "$mid" ] || { echo "tether: intro send failed (check creds / base url / agent protection)"; exit 1; }
    t_state_set armed 1 to "$to" conversation_id "$conv" last_message_id "$mid" \
      last_poll "$(t_now_iso)" project "$proj" started_at "$(t_now_iso)"
    echo "tether: started — thread ${conv} → ${to} (intro sent, ${mid})"
    ;;

  update)
    need_config; need_armed
    msg="${1:-}"; [ -n "$msg" ] || { echo "usage: tether.sh update \"<message>\""; exit 2; }
    rid="$(t_state_get last_message_id)"
    mid="$(t_api_reply "$rid" "$msg")"
    if [ -z "$mid" ]; then echo "tether: update send failed"; exit 1; fi
    t_state_set last_message_id "$mid"
    echo "tether: update sent (${mid})"
    ;;

  poll)
    need_config; need_armed
    t_poll_once
    ;;

  ask)
    # Email a question into the thread and BLOCK until the user replies, then
    # print the answer. This is how a tethered agent asks the user anything —
    # over email, never a terminal prompt the AFK user can't see. Run it in the
    # background and wait for the completion notification.
    need_config; need_armed
    q="${1:-}"; [ -n "$q" ] || { echo "usage: tether.sh ask \"<question>\""; exit 2; }
    rid="$(t_state_get last_message_id)"
    mid="$(t_api_reply "$rid" "❓ ${q}

(Reply to this email with your answer — I'll wait for it.)")"
    [ -n "$mid" ] || { echo "tether: ask send failed"; exit 1; }
    t_state_set last_message_id "$mid"
    echo "tether: question sent (${mid}); waiting for your reply…"
    max="${E2A_TETHER_ASK_TIMEOUT:-1800}"; interval="${E2A_TETHER_POLL_INTERVAL:-20}"; elapsed=0
    while [ "$elapsed" -lt "$max" ]; do
      sleep "$interval"; elapsed=$((elapsed + interval))
      out="$(t_poll_once)"
      [ "$out" = "(no new replies)" ] || { echo "$out"; exit 0; }
    done
    echo "tether: ask timed out after ${max}s with no answer"; exit 3
    ;;

  status)
    if t_load_config; then echo "config: OK (agent ${E2A_AGENT_EMAIL}, base ${E2A_BASE_URL})"; else echo "config: MISSING"; fi
    if [ "$(t_state_get armed)" = "1" ]; then
      echo "armed:  yes"
      echo "thread: $(t_state_get conversation_id) → $(t_state_get to)"
      echo "since:  $(t_state_get last_poll)"
    else
      echo "armed:  no"
    fi
    ;;

  stop)
    if [ "$(t_state_get armed)" = "1" ] && t_load_config; then
      rid="$(t_state_get last_message_id)"
      [ -n "$rid" ] && t_api_reply "$rid" "Tether ended. Session is no longer listening." >/dev/null 2>&1 || true
    fi
    t_state_clear
    echo "tether: stopped"
    ;;

  _selftest)
    echo "# unconfigured status (must not crash):"
    env -u E2A_API_KEY -u E2A_AGENT_EMAIL HOME=/nonexistent TETHER_STATE=/tmp/tether-selftest.json \
      bash "${here}/tether.sh" status
    rm -f /tmp/tether-selftest.json
    bash -n "${here}/lib.sh" && bash -n "${here}/tether.sh" && echo "# syntax OK"
    ;;

  ""|help|-h|--help)
    grep '^#' "${here}/tether.sh" | sed 's/^# \{0,1\}//' | sed -n '2,20p'
    ;;

  *) echo "tether: unknown command '$cmd' (try: start|update|poll|status|stop)"; exit 2;;
esac
