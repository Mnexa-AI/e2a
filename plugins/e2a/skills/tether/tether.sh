#!/usr/bin/env bash
# tether.sh — the runtime CLI the agent calls to stay in touch over email.
#
#   tether.sh start <email> [--title "<work>"] [--for 2h|8h|30m|1d] [--until <ISO>]  open + arm
#   tether.sh update "<message>"   send a threaded update ("as you see fit")
#   tether.sh update --html <file> send an HTML update (+ auto text fallback)
#   tether.sh ask "<question>"     email a question and BLOCK until the reply
#   tether.sh listen [--awake]     poll until a reply OR the window ends (bg it)
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
    # start <email> [--title "<work>"] [--for 30m|2h|8h|1d] [--until <ISO>]
    need_config
    to=""; forarg=""; untilarg=""; title=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --title) title="${2:-}"; shift 2;;
        --for) forarg="${2:-}"; shift 2;;
        --until) untilarg="${2:-}"; shift 2;;
        *) to="$1"; shift;;
      esac
    done
    [ -n "$to" ] || { echo "usage: tether.sh start <your-email> [--title \"<work>\"] [--for 2h|8h|30m|1d] [--until <ISO>]"; exit 2; }
    expires=""
    if [ -n "$untilarg" ]; then expires="$untilarg"
    elif [ -n "$forarg" ]; then
      expires="$(t_duration_to_expiry "$forarg")"
      [ "$expires" = "INVALID" ] && { echo "tether: can't parse --for '$forarg' — use a single unit (30m, 2h, 8h, 1d). Omit --for for no time limit."; exit 2; }
    fi
    window="${forarg:-${untilarg:-until you say stop}}"
    proj="$(basename "$PWD")"
    conv="tether-$(date +%s)-$$"
    # Subject = the thread's one stable title (threading replies onto it), so
    # make it say what the session is DOING, not just where it's running.
    subject="Tether: ${proj}"
    [ -n "$title" ] && subject="Tether: ${proj} — ${title}"
    intro="🪢 Tethered — ${proj}${title:+ — ${title}}

This session is now tethered (${window}). I'll send updates to this thread as I
make meaningful progress, and I'll pick up your replies. Reply any time with a
question or instruction; reply \"stop\" to end early.

— your coding agent"
    mid="$(t_api_send "$to" "$subject" "$intro" "$conv")"; rc=$?
    [ -n "$mid" ] || { echo "tether: intro send failed (check creds / base url / agent protection)"; exit 1; }
    if [ "$rc" = "2" ]; then
      echo "tether: intro was HELD for review (pending_review) — the user won't receive it, so NOT arming."
      echo "        Turn send-side protection/HITL OFF on ${E2A_AGENT_EMAIL}, then run start again."
      exit 1
    fi
    t_state_set armed 1 to "$to" conversation_id "$conv" last_message_id "$mid" \
      last_poll "$(t_now_iso)" project "$proj" started_at "$(t_now_iso)" expires_at "$expires"
    echo "tether: started — thread ${conv} → ${to} (intro ${mid}); window: ${window}${expires:+ (until ${expires})}"
    ;;

  update)
    # update "<text>"                       plain-text update
    # update --html <file> [--text "<t>"]   HTML update (+ optional text fallback)
    need_config; need_armed
    htmlfile=""; textarg=""; msg=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --html) htmlfile="${2:-}"; shift 2;;
        --text) textarg="${2:-}"; shift 2;;
        *) msg="$1"; shift;;
      esac
    done
    html=""
    if [ -n "$htmlfile" ]; then
      [ -f "$htmlfile" ] || { echo "tether: --html file not found: $htmlfile"; exit 2; }
      html="$(cat "$htmlfile")"
      # plain-text fallback: explicit --text/positional, else derived from the HTML
      [ -n "$msg" ] || msg="$textarg"
      [ -n "$msg" ] || msg="$(printf '%s' "$html" | t_html_to_text)"
    fi
    [ -n "$msg" ] || { echo "usage: tether.sh update \"<text>\"  |  update --html <file> [--text \"<fallback>\"]"; exit 2; }
    rid="$(t_state_get last_message_id)"
    mid="$(t_api_reply "$rid" "$msg" "$html")"; rc=$?
    if [ -z "$mid" ]; then echo "tether: update send failed"; exit 1; fi
    t_state_set last_message_id "$mid"
    if [ "$rc" = "2" ]; then
      echo "tether: WARNING update HELD for review (pending_review) — it did NOT reach the user. Disable send-side protection on ${E2A_AGENT_EMAIL}."
      exit 2
    fi
    echo "tether: update sent (${mid})$([ -n "$html" ] && echo ' [html]')"
    ;;

  poll)
    need_config; need_armed
    t_poll_once
    ;;

  listen)
    # Deadline-bounded poller. Polls until a reply (prints REPLY_RECEIVED + exits)
    # or until the window (expires_at) ends (prints TETHER_EXPIRED + exits). Run
    # it in the BACKGROUND: on a reply-exit, act then relaunch for the remaining
    # window; on TETHER_EXPIRED, run `stop`. Cheap (curl only, no tokens).
    #   --awake  keep the machine from IDLE-sleeping while listening (macOS
    #            caffeinate; released when listen exits). Does NOT survive the
    #            lid closing.
    need_config; need_armed
    awake=0
    while [ $# -gt 0 ]; do case "$1" in --awake) awake=1; shift;; *) shift;; esac; done
    if [ "$awake" = "1" ]; then
      if command -v caffeinate >/dev/null 2>&1; then
        caffeinate -i -w "$$" >/dev/null 2>&1 &   # dies with this listen process
        echo "tether: keeping the machine awake (caffeinate, idle-sleep off) while listening" >&2
      else
        echo "tether: --awake unsupported here (no caffeinate); machine may idle-sleep" >&2
      fi
    fi
    interval="${E2A_TETHER_POLL_INTERVAL:-20}"
    while :; do
      rem="$(t_remaining_seconds)"
      if [ "$rem" -le 0 ]; then echo "TETHER_EXPIRED"; exit 0; fi
      # An `ask` is blocking on the same inbox — don't poll, or we'd consume the
      # answer it's waiting for. Idle until the ask releases the lock.
      if t_ask_active; then
        s="$interval"; [ "$rem" -lt "$s" ] && s="$rem"; sleep "$s"; continue
      fi
      out="$(t_poll_once)"
      if [ "$out" != "(no new replies)" ]; then echo "REPLY_RECEIVED:"; echo "$out"; exit 0; fi
      s="$interval"; [ "$rem" -lt "$s" ] && s="$rem"
      sleep "$s"
    done
    ;;

  ask)
    # Email a question into the thread and BLOCK until the user replies, then
    # print the answer. This is how a tethered agent asks the user anything —
    # over email, never a terminal prompt the AFK user can't see. Run it in the
    # background and wait for the completion notification.
    need_config; need_armed
    q="${1:-}"; [ -n "$q" ] || { echo "usage: tether.sh ask \"<question>\""; exit 2; }
    # Hold the ask lock for this whole invocation so a background `listen` pauses
    # and doesn't steal the answer; released on any exit (reply, timeout, error).
    trap 't_ask_end' EXIT INT TERM
    t_ask_begin
    rid="$(t_state_get last_message_id)"
    mid="$(t_api_reply "$rid" "❓ ${q}

(Reply to this email with your answer — I'll wait for it.)")"; rc=$?
    [ -n "$mid" ] || { echo "tether: ask send failed"; exit 1; }
    t_state_set last_message_id "$mid"
    if [ "$rc" = "2" ]; then
      echo "tether: WARNING question HELD for review (pending_review) — it did NOT reach the user, so not waiting. Disable send-side protection on ${E2A_AGENT_EMAIL}."
      exit 4
    fi
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
      exp="$(t_state_get expires_at)"
      if [ -n "$exp" ]; then
        rem="$(t_remaining_seconds)"
        if [ "$rem" -gt 0 ]; then echo "window: until ${exp} (${rem}s left)"; else echo "window: EXPIRED (${exp})"; fi
      else
        echo "window: until you stop"
      fi
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

    fail=0
    ck() { if [ "$2" = "$3" ]; then echo "ok: $1"; else echo "FAIL: $1 (want [$3] got [$2])"; fail=1; fi; }

    echo "# duration parser:"
    ck "2h parses"        "$([ -n "$(t_duration_to_expiry 2h)" ] && echo good)" "good"
    ck "'' → until-stop"  "$(t_duration_to_expiry '')"     ""
    ck "1h30m → INVALID"  "$(t_duration_to_expiry '1h30m')" "INVALID"
    ck "'90 min'→INVALID" "$(t_duration_to_expiry '90 min')" "INVALID"

    echo "# placeholder creds are treated as MISSING:"
    ph="$(env E2A_API_KEY='e2a_agt_...' E2A_AGENT_EMAIL='tether@you.example' \
      HOME=/nonexistent bash "${here}/tether.sh" status | grep -c 'config: MISSING')"
    ck "unfilled template → MISSING" "$ph" "1"

    echo "# ask/listen mutex:"
    ( export TETHER_STATE=/tmp/tether-selftest-lock.json
      t_ask_active && { echo "FAIL: lock present before begin"; exit 1; }
      t_ask_begin;  t_ask_active || { echo "FAIL: lock absent after begin"; exit 1; }
      t_ask_end;   ! t_ask_active || { echo "FAIL: lock present after end"; exit 1; }
      echo "ok: ask lock begin/active/end" ) || fail=1
    rm -f /tmp/tether-selftest-lock.json /tmp/ask.lock

    bash -n "${here}/lib.sh" && bash -n "${here}/tether.sh" && echo "# syntax OK"
    [ "$fail" = "0" ] && echo "# selftest PASS" || { echo "# selftest FAIL"; exit 1; }
    ;;

  ""|help|-h|--help)
    grep '^#' "${here}/tether.sh" | sed 's/^# \{0,1\}//' | sed -n '2,20p'
    ;;

  *) echo "tether: unknown command '$cmd' (try: start|update|poll|status|stop)"; exit 2;;
esac
