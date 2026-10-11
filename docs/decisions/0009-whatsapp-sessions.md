# ADR 0009: Isolated WhatsApp session database with exclusive advisory file locking

Status: accepted. Date: 2026-10-10.

## Context

The whatsmeow WhatsApp Web library requires persistent SQLite storage for cryptographic identity keys, device tokens, and pre-keys. Storing session state in the application election database would couple external library schemas to domain data, complicate backup boundaries, and risk concurrent corruption during pairing or logout operations.

## Decision

Isolate the WhatsApp session store in a separate SQLite database file (`WHATSAPP_SESSION_DB_PATH`, defaulting to `/data/whatsapp-session.db`), distinct from the primary election database (`/data/elections.db`). Protect the file with strict permissions (mode 0600, UID 65532).

Enforce cross-process mutual exclusion with an advisory file lock (`whatsapp-session.db.lock` using POSIX `flock`) shared by the application runtime daemon, `whatsapp pair`, and `whatsapp logout` subcommands. Require exactly one stored device upon daemon startup: zero devices permits new pairing, one restores existing authentication, and more than one fails closed to prevent account ambiguity.

## Consequences

- Operator pairing and logout CLI commands cannot run concurrently with the background daemon or corrupt device keys.
- Sensitive authentication credentials remain segregated from election data, enabling targeted backup exclusion, encryption, and permission auditing.
- Offline pairing requirement: pairing or logging out requires stopping the daemon container (`docker compose stop election-bot`) before running the CLI command (`docker compose run --rm -it election-bot whatsapp pair`).
- Corrupted or ambiguous multi-device stores fail safely, requiring explicit operator cleanup rather than guessing which identity to restore.

See [architecture](../architecture.md) and [specification](../superpowers/specs/2026-10-10-whatsapp-integration-design.md).
