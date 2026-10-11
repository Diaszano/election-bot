# ADR 0008: Channel-aware durable notifications and separate channel delivery workers

Status: accepted. Date: 2026-10-10.

## Context

The election-bot system was initially designed for Telegram delivery. Adding WhatsApp delivery introduces different rate limits, connection lifecycles, message formatting constraints, and failure modes. Coupling delivery pipelines could allow WhatsApp connection issues or rate limiting to delay or block Telegram subscribers, or vice versa.

## Decision

Extend domain identities to be channel-aware (`subscription.Identity` combining election identity, channel enum, and canonical recipient string) and migrate the outbox schema to channel-scoped records (`migrations/002_channel_notifications.sql`).

Preserve a single modular monolith with a single polling worker, but instantiate independent, asynchronous delivery publisher workers for Telegram and WhatsApp. Transactions remain short and localized to SQLite; neither network calls nor external SDK operations occur inside database transactions.

## Consequences

- Delivery isolation: WhatsApp connection interruptions, reconnect backoffs, or rate limits cannot stall Telegram delivery or election polling.
- Zero distributed coordination: SQLite remains the single application database with independent serial queue processors per channel.
- Per-channel and per-recipient durable pacing: cooldowns and retry schedules are tracked independently in the database.
- Scaling beyond a single instance remains out of scope without external job coordination.

See [architecture](../architecture.md) and [specification](../superpowers/specs/2026-10-10-whatsapp-integration-design.md).
