# Contributing to election-bot

Thank you for contributing to `election-bot`! We welcome contributions to improve the bot, documentation, observability, and infrastructure.

## Governance and Code Ownership

This repository is maintained by [@Diaszano](https://github.com/Diaszano). All code changes must go through standard code review via Pull Requests.

---

## Branch Naming Conventions

Branches should be named using the format `<type>/<short-kebab-case-description>`:

- `feat/<description>` — New features (e.g., `feat/tse-election-client`)
- `fix/<description>` — Bug fixes (e.g., `fix/sqlite-connection-leak`)
- `docs/<description>` — Documentation improvements (e.g., `docs/update-architecture`)
- `style/<description>` — Code formatting or whitespace changes
- `refactor/<description>` — Code restructuring without feature or bug changes
- `perf/<description>` — Performance optimizations
- `test/<description>` — Adding or updating test suites
- `build/<description>` — Build system or dependency updates
- `ci/<description>` — CI/CD workflows and automation
- `chore/<description>` — Maintenance chores
- `hotfix/<description>` — Urgent production bug fixes

Exempt branches include `main`, `master`, and automated tool branches (`release-please--*`, `dependabot/*`).

---

## Commit Message Conventions

This project strictly follows the [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/) specification:

```text
<type>[optional scope][!]: <description>

[optional body]

[optional footer(s)]
```

Allowed types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.

### Examples

- `feat(whatsapp): implement QR reconnect circuit breaker`
- `fix(tse): guard against empty presidential runoff candidate list`
- `docs(adr): document decision on single-instance SQLite lock`
- `chore(deps): bump go.mau.fi/whatsmeow from 0.0.0 to 0.1.0`

---

## Local Development and Testing

Before opening a pull request, ensure all linters, tests, and verifications pass:

```bash
# Verify branch name and release configuration
make check

# Run unit tests
make test

# Run race detector tests
make test-race

# Run linter checks (golangci-lint)
make lint

# Run offline deterministic simulation
make simulate

# Run complete CI verification suite locally
make ci

# Inspect binary version output
make version
```

---

## Pull Request Guidelines

1. **Keep Pull Requests Focused**: Limit changes to a single concern or feature.
2. **Include Tests**: Add unit tests or integration tests covering your changes.
3. **Preserve Domain Rules**:
   - The presidential runoff gate must fail-closed: round 1 results cannot be declared definitive without official TSE mathematically defined status (`definido = "s"`).
   - WhatsApp session persistence must follow single-instance constraints to prevent database corruption.
4. **No Secrets or Credentials**: Never commit API keys, authentication tokens, or WhatsApp session SQLite files.
