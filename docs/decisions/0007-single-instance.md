# ADR 0007: Deploy one application instance

Status: accepted. Date: 2026-10-10.

## Context

The approved election-bot specification requires a maintainable single-election modular monolith with durable Telegram recovery.

## Decision

Separate in-process poll/publish workers and one SQLite connection keep scheduling and recovery simple.

## Consequences

No brokers/distributed locking. Horizontal scaling requires a reviewed persistence/scheduling replacement.

See [architecture](../architecture.md) and [specification](../superpowers/specs/2026-10-10-election-bot-design.md).
