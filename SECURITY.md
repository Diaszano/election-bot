# Security Policy

## Supported Versions

Only the latest release running on the `main` branch is actively supported with security updates and patches.

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |
| < 0.1.0 | :x:                |

---

## Reporting a Vulnerability

If you discover a security vulnerability in `election-bot`, please **do not** open a public GitHub issue.

Instead, please report security vulnerabilities responsibly via private disclosure:

1. **GitHub Security Advisory**: Use the "Report a vulnerability" button in the repository's [Security Advisories tab](https://github.com/Diaszano/election-bot/security/advisories/new).
2. **Direct Contact**: If private advisories are unavailable, contact [@Diaszano](https://github.com/Diaszano) directly via GitHub profile contact methods.

### What to Include

Please provide as much information as possible:
- A description of the vulnerability and its potential impact.
- Step-by-step instructions or proof-of-concept (PoC) code to reproduce the issue safely.
- Any affected components or dependencies.

You will receive an acknowledgement within 48 hours, followed by updates on the investigation and resolution timeline.

---

## Supply Chain & Dependency Security

- Dependencies are monitored and updated weekly using [Dependabot](https://github.com/dependabot).
- Releases and builds run inside hardened GitHub Actions environments with pinned actions.
- Artifact builds enforce `go mod verify` and `CGO_ENABLED=0` to minimize binary attack surface.
