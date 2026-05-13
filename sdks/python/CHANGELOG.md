# Changelog

## Unreleased

### Added
- `InboundEmail.reply_to` and `AsyncInboundEmail.reply_to` (`list[str]`) — the
  parsed `Reply-To:` header from the inbound message, surfaced as a first-class
  field so consumers no longer need to re-parse `raw_message` with stdlib
  `email.message_from_bytes()`. Empty list when the header is absent; the SDK
  never silently falls back to `sender`. Use this when the sender is a no-reply
  notifications mailbox (Granola, GitHub, CI bots) and you need the actual
  correspondent.
- `MessageSummary.reply_to` (`list[str]`) on the REST polling path — the list
  endpoint now mirrors the same field.
- `reply_to` added to `unverified_payload` for forensic inspection without
  unlocking gated access.

### Reply-To trust path (decision)
`reply_to` is bound by e2a's HMAC the same way `to` and `cc` are: the
signature covers `SHA-256(raw_message)`, and `Reply-To:` lives in the raw
RFC 2822 bytes — so any rewrite invalidates `verify_signature()`. Trust the
field after `verify_signature()` succeeds (or via `client.get_message(...)`,
which uses the authenticated REST channel).

**What is _not_ guaranteed:** upstream-DKIM coverage of `Reply-To:`. If the
original sender's DKIM signature did not sign `Reply-To` (whether because
they didn't sign it, or there was no DKIM at all), a MITM between sender
and e2a could have rewritten the header without detection. e2a does not
re-verify or surface per-header DKIM coverage today — the
`Authentication-Results` / SPF/DKIM surface is unchanged. For routing
decisions where attacker-controlled `Reply-To` would matter, also confirm
`email.is_verified` and that the sender's domain is one you expect.

We chose to keep `reply_to` populated whenever it's present (rather than
masking it on partially-trusted messages or exposing a `reply_to_signed`
flag) so the field shape stays uniform with `to`/`cc` and consumers can
make their own policy decision. The trust model is documented on the
property docstring.

### Wire change
The webhook payload schema now includes an optional `reply_to: string[]`
field. Existing consumers that ignore unknown fields are unaffected; older
SDK versions parsing the same payload continue to work and simply do not
see the new key.
