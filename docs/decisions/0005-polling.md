# ADR 0005: Use conditional scheduled polling

Status: accepted. Date: 2026-10-10.

## Context

The approved election-bot specification requires a maintainable single-election modular monolith with durable Telegram recovery.

## Decision

Use ETag/Last-Modified with accepted representation persistence, configurable intervals and server cooldown/cache minima. IDs are opaque.

## Consequences

No aggressive 404 retry. Production remains gated on verified runoff contracts.

See [architecture](../architecture.md) and [specification](../superpowers/specs/2026-10-10-election-bot-design.md).
