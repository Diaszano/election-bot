# ADR 0004: Use SQLite and a transactional outbox

Status: accepted. Date: 2026-10-10.

## Context

The approved election-bot specification requires a maintainable single-election modular monolith with durable Telegram recovery.

## Decision

WAL, full synchronous writes and short transactions commit accepted snapshots with durable jobs. Telegram delivery occurs outside SQL transactions.

## Consequences

Single replica. At-least-once remote sends can duplicate after ambiguous success.

See [architecture](../architecture.md) and [specification](../superpowers/specs/2026-10-10-election-bot-design.md).
