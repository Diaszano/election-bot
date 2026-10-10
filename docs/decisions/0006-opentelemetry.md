# ADR 0006: Use official OpenTelemetry APIs and SDKs

Status: accepted. Date: 2026-10-10.

## Context

The approved election-bot specification requires a maintainable single-election modular monolith with durable Telegram recovery.

## Decision

OTLP trace/metric/log exporters plus slog bridge deliver asynchronously to Collector and Grafana backends. Privacy-safe Telegram spans avoid token-bearing URLs.

## Consequences

Export failures are nonfatal; operator signal retention and paging require configuration.

See [architecture](../architecture.md) and [specification](../superpowers/specs/2026-10-10-election-bot-design.md).
