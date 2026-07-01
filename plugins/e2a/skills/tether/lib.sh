#!/usr/bin/env bash
# lib.sh — shared config + e2a helpers for the tether skill.
#
# Config comes from the environment, falling back to ~/.e2a-tether.env.
# Required: E2A_API_KEY, E2A_AGENT_EMAIL. The recipient is supplied at
# `tether.sh start <email>` and kept in the state file.

t_load_config() {
  # 1) explicit tether config
  if [ -z "${E2A_API_KEY:-}" ] && [ -f "${HOME}/.e2a-tether.env" ]; then
    # shellcheck disable=SC1091
    set -a; . "${HOME}/.e2a-tether.env"; set +a
  fi
  # 2) reuse the CLI's agent creds from `e2a login` (~/.e2a/config.json)
  if [ -z "${E2A_API_KEY:-}" ] && [ -f "${HOME}/.e2a/config.json" ]; then
    eval "$(python3 -c 'import json,shlex,os
try:
  d=json.load(open(os.path.expanduser("~/.e2a/config.json")))
  if d.get("api_key"):     print("export E2A_API_KEY="+shlex.quote(d["api_key"]))
  if d.get("agent_email"): print("export E2A_AGENT_EMAIL="+shlex.quote(d["agent_email"]))
  if d.get("api_url"):     print("export E2A_BASE_URL="+shlex.quote(d["api_url"].rstrip("/")))
except Exception:pass')"
  fi
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

t_state_clear() { rm -f "$(t_state_path)"; }

# --- duration / expiry -------------------------------------------------------

# t_duration_to_expiry <dur> → ISO expires_at (empty = no expiry / "until stop")
# accepts: 30m, 2h, 8h, 1d ; "" / forever / until-stop → empty
t_duration_to_expiry() {
  python3 -c 'import sys,datetime,re
d=(sys.argv[1] if len(sys.argv)>1 else "").strip().lower()
if not d or d in ("forever","none","off","until-stop","stop"):print("");raise SystemExit
m=re.fullmatch(r"(\d+)\s*([mhd])",d)
if not m:print("");raise SystemExit
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

# t_api_send <to> <subject> <body> <conversation_id> → prints message_id
t_api_send() {
  local email resp status
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
  [ "$status" = "pending_review" ] && echo "tether: WARNING send held (pending_review) — disable protection on ${E2A_AGENT_EMAIL}" >&2
  printf '%s' "$resp" | python3 -c 'import json,sys
try:print(json.load(sys.stdin).get("message_id",""))
except Exception:print("")'
}

# t_api_reply <in_reply_to_id> <body> [html_body] → prints new message_id
t_api_reply() {
  local email resp
  email="$(t_urlencode "$E2A_AGENT_EMAIL")"
  resp="$(curl -sS -m 30 -X POST \
    -H "Authorization: Bearer ${E2A_API_KEY}" -H "Content-Type: application/json" \
    -d "$(python3 -c 'import json,sys
p={"body":sys.argv[1]}
if len(sys.argv)>2 and sys.argv[2]:p["html_body"]=sys.argv[2]
print(json.dumps(p))' "$2" "${3:-}")" \
    "${E2A_BASE_URL}/v1/agents/${email}/messages/${1}/reply" 2>/dev/null)" || return 1
  printf '%s' "$resp" | python3 -c 'import json,sys
try:print(json.load(sys.stdin).get("message_id",""))
except Exception:print("")'
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
    t_state_set last_message_id "$id"
  done <<< "$rows"
  t_state_set last_poll "$advance"
  [ "$n" -gt 0 ] || echo "(no new replies)"
}
