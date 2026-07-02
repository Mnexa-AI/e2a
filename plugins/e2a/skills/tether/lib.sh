#!/usr/bin/env bash
# lib.sh — shared config + e2a helpers for the tether skill.
#
# Config comes from the environment, falling back to ~/.e2a-tether.env.
# Required: E2A_API_KEY, E2A_AGENT_EMAIL. The recipient is supplied at
# `tether.sh start <email>` and kept in the state file.

t_load_config() {
  # Explicit env vars win; each fallback source fills only the vars still missing
  # (so exporting just E2A_API_KEY in the env doesn't skip an email set in the file).
  local envk="${E2A_API_KEY:-}" enve="${E2A_AGENT_EMAIL:-}" envu="${E2A_BASE_URL:-}"

  # 1) explicit tether config
  if { [ -z "${E2A_API_KEY:-}" ] || [ -z "${E2A_AGENT_EMAIL:-}" ]; } && [ -f "${HOME}/.e2a-tether.env" ]; then
    # shellcheck disable=SC1091
    set -a; . "${HOME}/.e2a-tether.env"; set +a
  fi
  # 2) reuse the CLI's agent creds from `e2a login` (~/.e2a/config.json)
  if { [ -z "${E2A_API_KEY:-}" ] || [ -z "${E2A_AGENT_EMAIL:-}" ]; } && [ -f "${HOME}/.e2a/config.json" ]; then
    eval "$(python3 -c 'import json,shlex,os
try:
  d=json.load(open(os.path.expanduser("~/.e2a/config.json")))
  if d.get("api_key"):     print("export E2A_API_KEY="+shlex.quote(d["api_key"]))
  if d.get("agent_email"): print("export E2A_AGENT_EMAIL="+shlex.quote(d["agent_email"]))
  if d.get("api_url"):     print("export E2A_BASE_URL="+shlex.quote(d["api_url"].rstrip("/")))
except Exception:pass')"
  fi
  # explicit env always wins over whatever a fallback source supplied
  [ -n "$envk" ] && E2A_API_KEY="$envk"
  [ -n "$enve" ] && E2A_AGENT_EMAIL="$enve"
  [ -n "$envu" ] && E2A_BASE_URL="$envu"

  # Treat the copied-but-unfilled tether.env.example placeholders as unset, so a
  # user who ran `cp … tether.env.example` without editing gets `config: MISSING`
  # (the new-user signal) instead of a bogus `config: OK` that fails at `start`.
  case "${E2A_API_KEY:-}" in *...*) E2A_API_KEY="";; esac
  case "${E2A_AGENT_EMAIL:-}" in *@*.example|*.example) E2A_AGENT_EMAIL="";; esac

  E2A_BASE_URL="${E2A_BASE_URL:-https://api.e2a.dev}"
  [ -n "${E2A_API_KEY:-}" ] && [ -n "${E2A_AGENT_EMAIL:-}" ]
}

t_now_iso()  { python3 -c 'import datetime;print(datetime.datetime.now(datetime.timezone.utc).isoformat())'; }
t_urlencode(){ python3 -c 'import sys,urllib.parse;print(urllib.parse.quote(sys.argv[1],safe=""))' "$1"; }

# --- state -------------------------------------------------------------------

t_state_path() { echo "${TETHER_STATE:-$HOME/.e2a-tether/state.json}"; }

t_state_get() {
  local f; f="$(t_state_path)"; [ -f "$f" ] || return 0
  python3 -c 'import json,sys
try:print(json.load(open(sys.argv[1])).get(sys.argv[2],"") or "")
except Exception:pass' "$f" "$1"
}

t_state_set() {  # t_state_set k1 v1 [k2 v2 ...]
  local f; f="$(t_state_path)"; mkdir -p "$(dirname "$f")"
  python3 -c 'import json,sys,os
f=sys.argv[1];kv=sys.argv[2:]
d={}
if os.path.exists(f):
  try:d=json.load(open(f))
  except Exception:d={}
for i in range(0,len(kv),2):d[kv[i]]=kv[i+1]
json.dump(d,open(f,"w"),indent=2)' "$f" "$@"
}

t_state_clear() { rm -f "$(t_state_path)" "$(t_ask_lock_path)"; }

# --- ask/listen mutex --------------------------------------------------------
# `ask` and `listen` both poll the same inbox and share one dedup cursor, so if
# they run at once whichever polls first consumes the reply — a running `listen`
# would eat the answer `ask` is blocking for, and `ask` would spin to timeout.
# While an `ask` is in flight it holds this lock; `listen` sees it and pauses
# polling so the answer flows to the blocked `ask`.

t_ask_lock_path() { echo "$(dirname "$(t_state_path)")/ask.lock"; }
t_ask_begin()  { local f; f="$(t_ask_lock_path)"; mkdir -p "$(dirname "$f")"; : > "$f"; }
t_ask_end()    { rm -f "$(t_ask_lock_path)"; }
t_ask_active() { [ -f "$(t_ask_lock_path)" ]; }

# --- duration / expiry -------------------------------------------------------

# t_duration_to_expiry <dur> → ISO expires_at (empty = no expiry / "until stop";
# "INVALID" = unparseable, so the caller can reject it instead of silently
# treating a mistyped "1h30m"/"90 min" as an unbounded window).
# accepts a SINGLE unit: 30m, 2h, 8h, 1d ; "" / forever / until-stop → empty
t_duration_to_expiry() {
  python3 -c 'import sys,datetime,re
d=(sys.argv[1] if len(sys.argv)>1 else "").strip().lower()
if not d or d in ("forever","none","off","until-stop","stop"):print("");raise SystemExit
m=re.fullmatch(r"(\d+)\s*([mhd])",d)
if not m:print("INVALID");raise SystemExit
secs=int(m.group(1))*{"m":60,"h":3600,"d":86400}[m.group(2)]
print((datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=secs)).isoformat())' "$1"
}

# t_remaining_seconds → seconds until expires_at; huge sentinel if no expiry
t_remaining_seconds() {
  local exp; exp="$(t_state_get expires_at)"
  [ -n "$exp" ] || { echo 2147483647; return; }
  python3 -c 'import sys,datetime
try:
  t=datetime.datetime.fromisoformat(sys.argv[1].replace("Z","+00:00"))
  print(int((t-datetime.datetime.now(datetime.timezone.utc)).total_seconds()))
except Exception:print(2147483647)' "$exp"
}

# --- e2a API -----------------------------------------------------------------

# t_api_send <to> <subject> <body> <conversation_id> → prints message_id;
# returns 2 (and warns on stderr) if the send was HELD (pending_review) so the
# caller can refuse to arm / not report a phantom "sent".
t_api_send() {
  local email resp status mid
  email="$(t_urlencode "$E2A_AGENT_EMAIL")"
  local payload
  payload="$(python3 -c 'import json,sys
print(json.dumps({"to":[sys.argv[1]],"subject":sys.argv[2],"body":sys.argv[3],"conversation_id":sys.argv[4]}))' \
    "$1" "$2" "$3" "$4")"
  resp="$(curl -sS -m 30 -X POST \
    -H "Authorization: Bearer ${E2A_API_KEY}" -H "Content-Type: application/json" \
    -d "$payload" "${E2A_BASE_URL}/v1/agents/${email}/messages" 2>/dev/null)" || return 1
  status="$(printf '%s' "$resp" | python3 -c 'import json,sys
try:print(json.load(sys.stdin).get("status",""))
except Exception:print("")')"
  mid="$(printf '%s' "$resp" | python3 -c 'import json,sys
try:print(json.load(sys.stdin).get("message_id",""))
except Exception:print("")')"
  printf '%s' "$mid"
  if [ "$status" = "pending_review" ]; then
    echo "tether: WARNING send held (pending_review) — disable protection on ${E2A_AGENT_EMAIL}" >&2
    return 2
  fi
}

# Attachment cap: 15 MB of raw bytes total per send. Base64 inflates ~4/3 and
# the server rejects requests over 25 MB, so 15 MB raw (~20 MB encoded) leaves
# headroom for the body; typical mail providers cap around 25 MB too.
T_ATTACH_MAX_BYTES=$((15 * 1024 * 1024))

# t_attach_check <file>... → 0 ok; 3 a file is missing; 4 over the total cap.
# Validates BEFORE encoding so callers can fail fast with a clear message.
t_attach_check() {
  python3 -c 'import sys,os
maxb=int(sys.argv[1]); total=0
for f in sys.argv[2:]:
    if not os.path.isfile(f):
        sys.stderr.write("tether: attachment not found: %s\n"%f); sys.exit(3)
    total+=os.path.getsize(f)
if total>maxb:
    sys.stderr.write("tether: attachments total %d bytes — over the %d MB cap; send a link instead\n"%(total,maxb//1048576)); sys.exit(4)' \
    "$T_ATTACH_MAX_BYTES" "$@"
}

# t_reply_payload <out_file> <body> <html> [file...] — write the reply JSON
# payload to <out_file>. Each file becomes {filename, content_type, data(b64)}
# per the API's attachments[] schema; MIME type is guessed from the filename
# (fallback application/octet-stream).
t_reply_payload() {
  python3 -c 'import json,sys,base64,mimetypes,os
out,body,html=sys.argv[1],sys.argv[2],sys.argv[3]
p={"body":body}
if html: p["html_body"]=html
atts=[]
for f in sys.argv[4:]:
    atts.append({"filename":os.path.basename(f),
                 "content_type":mimetypes.guess_type(f)[0] or "application/octet-stream",
                 "data":base64.b64encode(open(f,"rb").read()).decode()})
if atts: p["attachments"]=atts
json.dump(p,open(out,"w"))' "$@"
}

# t_api_reply <in_reply_to_id> <body> [html_body] [attach_file ...] → prints new message_id;
# returns 2 (and warns on stderr) if the reply was HELD (pending_review). The
# reply path — every update/ask/stop — used to drop `status`, so a held update
# printed a phantom "sent" while the user's inbox stayed empty.
# Optional args past html_body are file paths attached to the reply. The payload
# goes to curl via a temp file, never argv — a multi-MB base64 attachment would
# blow past ARG_MAX if passed as a command-line argument.
t_api_reply() {
  local email resp status mid rid body html pf
  rid="$1"; body="$2"; html="${3:-}"
  shift 2; [ $# -gt 0 ] && shift   # remaining args = attachment file paths
  email="$(t_urlencode "$E2A_AGENT_EMAIL")"
  pf="$(mktemp "${TMPDIR:-/tmp}/tether-payload.XXXXXX")" || return 1
  t_reply_payload "$pf" "$body" "$html" "$@" || { rm -f "$pf"; return 1; }
  resp="$(curl -sS -m 120 -X POST \
    -H "Authorization: Bearer ${E2A_API_KEY}" -H "Content-Type: application/json" \
    --data-binary "@${pf}" \
    "${E2A_BASE_URL}/v1/agents/${email}/messages/${rid}/reply" 2>/dev/null)" || { rm -f "$pf"; return 1; }
  rm -f "$pf"
  status="$(printf '%s' "$resp" | python3 -c 'import json,sys
try:print(json.load(sys.stdin).get("status",""))
except Exception:print("")')"
  mid="$(printf '%s' "$resp" | python3 -c 'import json,sys
try:print(json.load(sys.stdin).get("message_id",""))
except Exception:print("")')"
  printf '%s' "$mid"
  if [ -z "$mid" ]; then
    # Distinguish "this anchor is not repliable" (404) from other failures so
    # t_reply_anchored can fall back to another anchor instead of giving up.
    local ecode
    ecode="$(printf '%s' "$resp" | python3 -c 'import json,sys
try:print((json.load(sys.stdin).get("error") or {}).get("code",""))
except Exception:print("")')"
    [ "$ecode" = "not_found" ] && return 3
    return 1
  fi
  if [ "$status" = "pending_review" ]; then
    echo "tether: WARNING reply held (pending_review) — disable protection on ${E2A_AGENT_EMAIL}" >&2
    return 2
  fi
}

# t_reply_anchored <body> <html> [attach_file ...] → send a threaded reply,
# trying anchors in order: the last message in the thread (best Gmail
# threading), then the user's last inbound reply, then the intro. Hosted APIs
# that predate reply-to-own-outbound (#360) 404 when the anchor is a
# reply-created outbound message — without this fallback, the SECOND of two
# consecutive updates with no user reply in between is silently undeliverable.
# Prints the new message_id; propagates t_api_reply's return code.
t_reply_anchored() {
  local body="$1" html="${2:-}"
  shift 1; [ $# -gt 0 ] && shift
  local tried="" rid mid rc
  for rid in "$(t_state_get last_message_id)" "$(t_state_get last_inbound_id)" "$(t_state_get intro_id)"; do
    [ -n "$rid" ] || continue
    case " $tried " in *" $rid "*) continue;; esac
    tried="$tried $rid"
    mid="$(t_api_reply "$rid" "$body" "$html" "$@")"; rc=$?
    [ "$rc" = "3" ] && continue   # anchor not repliable here — try the next one
    printf '%s' "$mid"; return $rc
  done
  echo "tether: no repliable anchor (tried:${tried})" >&2
  return 3
}

# strip HTML tags → a plain-text fallback (crude but fine for email body)
t_html_to_text() {
  python3 -c 'import sys,re,html
t=sys.stdin.read()
t=re.sub(r"(?is)<(script|style).*?</\1>"," ",t)
t=re.sub(r"(?i)<(br|/p|/div|/li|/tr|/h[1-6])\s*/?>","\n",t)
t=re.sub(r"<[^>]+>"," ",t)
t=html.unescape(t)
print(re.sub(r"[ \t]+"," ",re.sub(r"\n\s*\n\s*","\n\n",t)).strip())'
}

# t_api_poll <conversation_id> <since_iso> → TSV lines: id<TAB>from<TAB>created_at (inbound, oldest first)
t_api_poll() {
  local email resp
  email="$(t_urlencode "$E2A_AGENT_EMAIL")"
  resp="$(curl -sS -m 30 -G \
    -H "Authorization: Bearer ${E2A_API_KEY}" \
    --data-urlencode "direction=inbound" --data-urlencode "read_status=all" \
    --data-urlencode "sort=asc" --data-urlencode "limit=20" \
    --data-urlencode "conversation_id=${1}" --data-urlencode "since=${2}" \
    "${E2A_BASE_URL}/v1/agents/${email}/messages" 2>/dev/null)" || return 1
  printf '%s' "$resp" | python3 -c 'import json,sys
try:
  for m in json.load(sys.stdin).get("items",[]):
    print("\t".join([m.get("message_id",""),m.get("from",""),m.get("created_at","")]))
except Exception:pass'
}

# t_api_body <message_id> → command text (parsed.text preferred; quoted history stripped)
t_api_body() {
  local email resp
  email="$(t_urlencode "$E2A_AGENT_EMAIL")"
  resp="$(curl -sS -m 30 -H "Authorization: Bearer ${E2A_API_KEY}" \
    "${E2A_BASE_URL}/v1/agents/${email}/messages/${1}" 2>/dev/null)" || return 1
  printf '%s' "$resp" | python3 -c 'import json,sys
try:
  d=json.load(sys.stdin)
  p=(d.get("parsed") or {}).get("text") or ""
  b=(d.get("body") or {}).get("text") or ""
  print((p or b).strip())
except Exception:print("")'
}

# --- dedup + poll core -------------------------------------------------------
# Replies are deduped by message-id (a `seen` set in state), NOT a bare time
# cursor. Email parsing is async: a just-arrived reply can have an empty
# parsed/body for a moment. We therefore do NOT mark such a message seen or
# advance the watermark past it — we retry it next poll (up to a max age),
# so a reply can never be silently skipped (the bug that dropped a real reply).

# seconds since an RFC3339 timestamp
t_age_seconds() {
  python3 -c 'import sys,datetime
try:
  t=datetime.datetime.fromisoformat(sys.argv[1].replace("Z","+00:00"))
  print(int((datetime.datetime.now(datetime.timezone.utc)-t).total_seconds()))
except Exception:print(0)' "$1"
}

t_seen_has() {  # <id> → 0 if already processed
  local f; f="$(t_state_path)"; [ -f "$f" ] || return 1
  python3 -c 'import json,sys
try:sys.exit(0 if sys.argv[2] in (json.load(open(sys.argv[1])).get("seen") or []) else 1)
except Exception:sys.exit(1)' "$f" "$1"
}

t_seen_add() {  # <id> → record as processed (cap at last 500)
  local f; f="$(t_state_path)"; mkdir -p "$(dirname "$f")"
  python3 -c 'import json,sys,os
f,i=sys.argv[1],sys.argv[2]
d={}
if os.path.exists(f):
  try:d=json.load(open(f))
  except Exception:d={}
s=d.get("seen") or []
if i not in s:s.append(i)
d["seen"]=s[-500:]
json.dump(d,open(f,"w"),indent=2)' "$f" "$1"
}

# t_poll_once → print any new replies (dedup + parse-race safe), else "(no new replies)"
# Advances the `last_poll` watermark only through the contiguous processed prefix,
# stopping at the first not-yet-parsed message so it is retried, never lost.
t_poll_once() {
  local conv since rows n advance id from created body age
  conv="$(t_state_get conversation_id)"; since="$(t_state_get last_poll)"
  rows="$(t_api_poll "$conv" "$since")" || return 1
  n=0; advance="$since"
  while IFS=$'\t' read -r id from created; do
    [ -n "$id" ] || continue
    if t_seen_has "$id"; then advance="$created"; continue; fi
    body="$(t_api_body "$id")"
    if [ -z "$body" ]; then
      age="$(t_age_seconds "$created")"
      if [ "$age" -gt 120 ]; then t_seen_add "$id"; advance="$created"; continue
      else break; fi   # not parsed yet — retry next poll, don't advance past it
    fi
    t_seen_add "$id"; advance="$created"; n=$((n+1))
    printf '── reply from %s @ %s ──\n%s\n\n' "$from" "$created" "$body"
    # last_inbound_id is the always-repliable fallback anchor for
    # t_reply_anchored (an inbound message is a valid reply target on every
    # API version; the agent's own reply-created outbound may not be).
    t_state_set last_message_id "$id" last_inbound_id "$id"
  done <<< "$rows"
  t_state_set last_poll "$advance"
  [ "$n" -gt 0 ] || echo "(no new replies)"
}
