## Summary

Briefly describe the change and its motivation.

## Pull Request Checklist

Please ensure the following standards are met before requesting review:

- [ ] **PR Title**: Follows [Conventional Commits 1.0.0](https://www.conventionalcommits.org/) format (e.g., `feat(whatsapp): add reconnect backoff`, `fix(tse): parse percentage safely`).
- [ ] **Branch Naming**: Branch follows `<type>/<short-kebab-case-description>` (e.g., `feat/tse-client`, `fix/polling-jitter`).
- [ ] **Tests**: Unit and integration tests pass locally (`make test-race` or `make ci`).
- [ ] **Lint & Standards**: Code passes `make lint` and formatting checks (`make check`).
- [ ] **Security**: No hardcoded secrets, API tokens, WhatsApp session databases, or private credentials committed.
- [ ] **Election Integrity Gate**: Any changes touching election results logic preserve the fail-closed presidential runoff gate (official TSE mathematically defined status `definido = "s"` required before announcing definitive round-1 outcome).

## Related Issues

Closes #
