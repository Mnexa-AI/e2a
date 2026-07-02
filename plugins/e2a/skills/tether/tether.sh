#!/usr/bin/env bash
# tether.sh — the runtime CLI the agent calls to stay in touch over email.
#
#   tether.sh start <email> [--for 2h|8h|30m|1d] [--until <ISO>]  open + arm
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
    # start <email> [--for 30m|2h|8h|1d] [--until <ISO>]
    need_config
    to=""; forarg=""; untilarg=""
    while [ $# -gt 0 ]; do
      case "$1" in
        --for) forarg="${2:-}"; shift 2;;
        --until) untilarg="${2:-}"; shift 2;;
        *) to="$1"; shift;;
      esac
    done
    [ -n "$to" ] || { echo "usage: tether.sh start <your-email> [--for 2h|8h|30m|1d] [--until <ISO>]"; exit 2; }
    expires=""
    if [ -n "$untilarg" ]; then expires="$untilarg"
    elif [ -n "$forarg" ]; then expires="$(t_duration_to_expiry "$forarg")"; fi
    window="${forarg:-${untilarg:-until you say stop}}"
    proj="$(basename "$PWD")"
    conv="tether-$(date +%s)-$$"
    intro="🪢 Tethered — ${proj}

This session is now tethered (${window}). I'll send updates to this thread as I
make meaningful progress, and I'll pick up your replies. Reply any time with a
question or instruction; reply \"stop\" to end early.

— your coding agent"
    mid="$(t_api_send "$to" "Tether: ${proj}" "$intro" "$conv")"
    [ -n "$mid" ] || { echo "tether: intro send failed (check creds / base url / agent protection)"; exit 1; }
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
    mid="$(t_api_reply "$rid" "$msg" "$html")"
    if [ -z "$mid" ]; then echo "tether: update send failed"; exit 1; fi
    t_state_set last_message_id "$mid"
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

  setup)
    # Zero-to-tethered bootstrap. Resolves account creds (e2a login / e2a_acct_
    # key) → ensures an agent inbox (creates one on the shared domain if none) →
    # forces HITL off → writes ~/.e2a-tether.env. No MCP server or dashboard trip
    # needed. Flags: --new (always create), --email <addr> (use/create this one).
    force_new=0; want=""
    while [ $# -gt 0 ]; do case "$1" in --new) force_new=1; shift;; --email) want="${2:-}"; shift 2;; *) shift;; esac; done
    E2A_BASE_URL="${E2A_BASE_URL:-https://api.e2a.dev}"
    boot="$(t_resolve_bootstrap)"
    [ -n "$boot" ] || { echo "tether setup: need an ACCOUNT credential (e2a_acct_… key or an 'e2a login' session). Run 'e2a login' or put an e2a_acct_ key in ~/.e2a-tether.env, then re-run."; exit 1; }
    E2A_API_KEY="$boot"   # setup's create/mint/protection calls need account scope
    echo "tether setup: base ${E2A_BASE_URL} (account credential resolved)"
    agent=""
    if [ -n "$want" ] && [ "$force_new" = "0" ]; then agent="$want"; fi
    if [ -z "$agent" ] && [ "$force_new" = "0" ]; then
      ags="$(t_api_agents)"
      agent="$(printf '%s\n' "$ags" | grep -E '^tether-' | head -1)"
      if [ -z "$agent" ] && [ "$(printf '%s\n' "$ags" | grep -c .)" = "1" ]; then
        agent="$(printf '%s\n' "$ags" | grep . | head -1)"
      fi
      [ -n "$agent" ] && echo "tether setup: reusing existing agent"
    fi
    if [ -z "$agent" ]; then
      cand="$want"
      if [ -z "$cand" ] || [ "${cand#*@}" = "$cand" ]; then
        sd="$(t_api_shared_domain)"
        [ -n "$sd" ] || { echo "tether setup: no shared_domain here — verify a custom domain first, then re-run with --email you@yourdomain"; exit 1; }
        local_part="${cand:-tether-$("$PY" -c 'import secrets;print(secrets.token_hex(3))')}"
        cand="${local_part}@${sd}"
      fi
      echo "tether setup: creating agent ${cand}…"
      agent="$(t_api_create_agent "$cand")"
      [ -n "$agent" ] || { echo "tether setup: agent create failed (slug taken/invalid, or key lacks account scope)"; exit 1; }
    fi
    echo "tether setup: agent = ${agent}"
    if [ "$(t_api_protection_off "$agent")" = "ok" ]; then
      echo "tether setup: HITL off (outbound gate=flag, scan=off)"
    else
      echo "tether setup: WARNING could not confirm HITL off — check protection in the dashboard" >&2
    fi
    # Mint a dedicated AGENT-scoped key (least privilege) and store THAT — not
    # the broad account key — so a leaked tether key can't touch the account.
    agtkey="$(t_api_create_agent_key "$boot" "$agent" "tether-$("$PY" -c 'import secrets;print(secrets.token_hex(2))')")"
    if [ -n "$agtkey" ]; then
      echo "tether setup: minted agent-scoped key (e2a_agt_…) bound to the inbox"
    else
      echo "tether setup: WARNING could not mint an agent key — storing the account key instead" >&2
      agtkey="$boot"
    fi
    t_write_env "$agtkey" "$agent"
    echo "tether setup: wrote ${HOME}/.e2a-tether.env"
    # export the resolved creds so the confirming subprocess doesn't inherit a
    # stale/empty value from what t_resolve_bootstrap read off the old file.
    export E2A_API_KEY="$agtkey" E2A_AGENT_EMAIL="$agent"
    bash "${here}/tether.sh" status
    echo "tether setup: ready → tether.sh start <your-email> --for 30m"
    ;;

  _selftest)
    echo "# unconfigured status (must not crash):"
    env -u E2A_API_KEY -u E2A_AGENT_EMAIL HOME=/nonexistent TETHER_STATE=/tmp/tether-selftest.json \
      bash "${here}/tether.sh" status
    rm -f /tmp/tether-selftest.json
    bash -n "${here}/lib.sh" && bash -n "${here}/tether.sh" && echo "# syntax OK"
    # interpreter must actually run — a real Python round-trip (catches the
    # Windows python3-shim / missing-interpreter case that syntax checks miss).
    echo "# interpreter: ${PY:-<none>}"
    [ -n "${PY:-}" ] || { echo "# FAIL: no working Python 3 on PATH (set E2A_PYTHON)"; exit 1; }
    _st="$(mktemp)"; TETHER_STATE="$_st" t_state_set probe ok >/dev/null 2>&1
    if [ "$(TETHER_STATE="$_st" t_state_get probe)" = "ok" ]; then echo "# python round-trip OK"
    else echo "# FAIL: Python state round-trip broke (interpreter ${PY:-<none>})"; rm -f "$_st"; exit 1; fi
    rm -f "$_st"
    ;;

  ""|help|-h|--help)
    grep '^#' "${here}/tether.sh" | sed 's/^# \{0,1\}//' | sed -n '2,20p'
    ;;

  *) echo "tether: unknown command '$cmd' (try: start|update|poll|status|stop)"; exit 2;;
esac
