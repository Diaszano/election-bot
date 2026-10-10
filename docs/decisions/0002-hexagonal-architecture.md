# ADR 0002: Use hexagonal architecture

Status: accepted. Date: 2026-10-10.

## Context

The approved election-bot specification requires a maintainable single-election modular monolith with durable Telegram recovery.

## Decision

Application depends on focused ports and domain; outbound adapters implement them. The composition root constructs adapters.

## Consequences

Domain cannot import Telegram, SQL drivers, HTTP clients or OTel.

See [architecture](../architecture.md) and [specification](../superpowers/specs/2026-10-10-election-bot-design.md).
