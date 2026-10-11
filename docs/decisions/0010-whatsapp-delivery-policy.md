# ADR 0010: WhatsApp plain-text formatting, 14-minute editing limit, digest deduplication, and at-least-once stable request IDs

Status: accepted. Date: 2026-10-10.

## Context

WhatsApp's transport behavior differs significantly from Telegram. The WhatsApp protocol strictly limits message editing to 14 minutes from original dispatch, does not support HTML tags, aggressively throttles accounts for rapid consecutive messaging, and provides no transactional atomicity across external network delivery and local database commits.

## Decision

1. **Rendering**: Format WhatsApp messages with dedicated plain-text and WhatsApp markdown styling (bold `*`, monospace ```` ``` ````) via `format_whatsapp.go`, avoiding HTML.
2. **Editing & Digests**: Enforce a strict 14-minute message edit age cutoff (`MaxEditAge = 14*time.Minute`). When the active main message is within 14 minutes and not marked unavailable, update it in place subject to minimum edit pacing (`WHATSAPP_EDIT_MIN_INTERVAL`, default 60s). Once past 14 minutes or upon edit failure, switch to periodic summary digests (`WHATSAPP_DIGEST_INTERVAL`, default 15m).
3. **Deduplication**: Match requested presentation revisions against accepted state; identical presentation revisions require no network calls (`ResolveAlreadyAccepted`).
4. **Stable Request IDs**: Before any network call, persist an immutable delivery attempt (`notification.Attempt`) with a cryptographically generated request ID. Retries of identical payloads reuse the request ID to allow transport-level deduplication; updated payloads generate new attempts.
5. **Authorization & Privacy**: Limit subscriptions strictly to private chats on an explicit allowlist (`WHATSAPP_ALLOWED_RECIPIENTS`) who send `!start`. Ignore group, community, or broadcast chats. Never export phone numbers, JIDs, QR tokens, or message text in telemetry.

## Consequences

- Delivery remains durable at-least-once: crashes or timeouts between network acceptance and database commitment can result in duplicated messages.
- After 14 minutes, message history accumulates periodic digest updates rather than continuously mutating the original message.
- Conservative pacing (minimum 3s per recipient, 1s global) and allowlisting reduce account ban risks, but because whatsmeow is an unofficial WhatsApp Web client, bans remain an inherent risk. The official WhatsApp Business Platform remains the recommended long-term solution for high-volume enterprise messaging.

See [architecture](../architecture.md) and [specification](../superpowers/specs/2026-10-10-whatsapp-integration-design.md).
