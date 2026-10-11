# CI/CD, Semantic Versioning, Docker Publishing, and Repository Standards Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver a professional, secure, and reproducible CI/CD pipeline for `election-bot` with Semantic Versioning 2.0.0, CLI version introspection, native Go commit and branch validation, multi-arch GHCR publishing, supply chain security attestations, and repository governance standards.

**Architecture:** A centralized `internal/platform/version` package injects build metadata into the CLI, OpenTelemetry `service.version`, and logs. A zero-dependency Go CLI tool (`cmd/dev-check`) enforces Conventional Commits 1.0.0 and branch naming rules in CI and local git hooks without Node.js. GitHub Actions workflows orchestrate CI validation, container security scans with Trivy, self-contained Release Please publishing with `GITHUB_TOKEN`, exact release commit verification, and multi-architecture Docker container publishing to GHCR.

**Tech Stack:** Go 1.27.0, GitHub Actions, Docker Buildx, Trivy, Release Please, OpenTelemetry SDK, GHCR, Anchore SBOM Action, GitHub Artifact Attestation.

**Spec:** [`docs/superpowers/specs/2026-10-11-ci-cd-release-pipeline-design.md`](../specs/2026-10-11-ci-cd-release-pipeline-design.md). Read it before executing any task; it defines the approved requirements, immutability rules, security gates, and failure recovery policies.

## Global Constraints

- Project, binary, image, and service: `election-bot`; module: `github.com/Diaszano/election-bot`.
- Code, comments, technical logs, workflow names, and documentation are in English; user-facing election and Telegram/WhatsApp messages remain in Brazilian Portuguese.
- Official presidential runoff mode remains strictly fail-closed until production EA20 schemas and national finalization semantics are verified (`DATA_SOURCE_MODE=simulation` required in CI/CD).
- Preserve existing Telegram and WhatsApp functionality, dual-database SQLite architecture (`elections.db`, `whatsapp-session.db`), outbox delivery, and POSIX advisory locking.
- Do not modify `migrations/001_initial.sql` or `migrations/002_channel_notifications.sql`.
- Initial planned release is strictly `v0.1.0`. Do not label the project production-ready or publish a real release during implementation.
- Commit incrementally on `ci/github-actions-release-pipeline`; do not push to remote GitHub, merge, deploy, or create actual GitHub releases or tags.

---

## File Ownership Map

| Task | Component | Files Created / Modified |
|---|---|---|
| Task 1 | Version Package & CLI Introspection | `internal/platform/version/version.go`, `version_test.go`, `cmd/election-bot/main.go`, `main_test.go`, `internal/platform/lifecycle/run.go` |
| Task 2 | Native Commit & Branch Validator | `cmd/dev-check/main.go`, `cmd/dev-check/main_test.go` |
| Task 3 | Release Please Configuration & Bootstrap Test | `release-please-config.json`, `.release-please-manifest.json`, `CHANGELOG.md`, `cmd/dev-check/release_test.go` |
| Task 4 | GitHub Actions CI Workflow | `.github/workflows/ci.yml` |
| Task 5 | Self-Contained Release & Publishing Workflow | `.github/workflows/release.yml` |
| Task 6 | Repository Standards & Developer Tooling | `.editorconfig`, `.gitattributes`, `.github/CODEOWNERS`, `.github/dependabot.yml`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/ISSUE_TEMPLATE/bug_report.yml`, `.github/ISSUE_TEMPLATE/feature_request.yml`, `CONTRIBUTING.md`, `SECURITY.md`, `Makefile` |
| Task 7 | Documentation & Verification Ledger | `docs/ci-cd.md`, `docs/architecture.md`, `docs/observability.md`, `docs/validation.md`, `README.md` |

---

### Task 1: Version Package and CLI Introspection

**Files:**
- Create: `internal/platform/version/version.go`
- Test: `internal/platform/version/version_test.go`
- Modify: `cmd/election-bot/main.go`, `cmd/election-bot/main_test.go`, `internal/platform/lifecycle/run.go`

**Interfaces:**
- Produces: `version.Info`, `version.Get() Info`, `version.String() string`, `version.JSON() (string, error)`
- Consumes: Go standard library (`runtime`, `runtime/debug`, `encoding/json`, `fmt`)

- [x] **Step 1: Write the failing tests**

```go
// internal/platform/version/version_test.go
package version_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Diaszano/election-bot/internal/platform/version"
)

func TestVersionDefaults(t *testing.T) {
	info := version.Get()
	if info.Application != "election-bot" {
		t.Fatalf("expected application election-bot, got %q", info.Application)
	}
	if info.Version == "" {
		t.Fatal("expected non-empty version")
	}
	if info.GoVersion == "" {
		t.Fatal("expected non-empty go version")
	}
	if info.OS == "" || info.Arch == "" {
		t.Fatal("expected non-empty OS and Arch")
	}
}

func TestVersionStringAndJSON(t *testing.T) {
	s := version.String()
	if !strings.Contains(s, "election-bot") {
		t.Fatalf("expected application name in string output: %s", s)
	}
	raw, err := version.JSON()
	if err != nil {
		t.Fatalf("failed to format JSON: %v", err)
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if data["application"] != "election-bot" {
		t.Fatalf("expected application election-bot, got %v", data["application"])
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/platform/version -run TestVersionDefaults`
Expected: FAIL with undefined package `version`

- [x] **Step 3: Implement minimal version package**

```go
// internal/platform/version/version.go
package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

type Info struct {
	Application string `json:"application"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Date        string `json:"date"`
	GoVersion   string `json:"go_version"`
	OS          string `json:"os"`
	Arch        string `json:"arch"`
	Compiler    string `json:"compiler"`
}

func Get() Info {
	v := Version
	c := Commit
	d := Date

	if bi, ok := debug.ReadBuildInfo(); ok {
		if v == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
		if c == "none" {
			for _, s := range bi.Settings {
				if s.Key == "vcs.revision" {
					c = s.Value
					break
				}
			}
		}
		if d == "unknown" {
			for _, s := range bi.Settings {
				if s.Key == "vcs.time" {
					d = s.Value
					break
				}
			}
		}
	}

	return Info{
		Application: "election-bot",
		Version:     v,
		Commit:      c,
		Date:        d,
		GoVersion:   runtime.Version(),
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Compiler:    runtime.Compiler,
	}
}

func String() string {
	info := Get()
	return fmt.Sprintf("election-bot %s\nCommit: %s\nGo: %s\nPlatform: %s/%s",
		info.Version, info.Commit, info.GoVersion, info.OS, info.Arch)
}

func JSON() (string, error) {
	data, err := json.MarshalIndent(Get(), "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
```

- [x] **Step 4: Wire CLI command and OpenTelemetry version**

In `cmd/election-bot/main.go`, support `election-bot version` and `election-bot version --json`.
In `internal/platform/lifecycle/run.go`, replace `"dev"` with `version.Get().Version` in `observability.Setup(ctx, c, version.Get().Version)` and log version in structured startup log.

- [x] **Step 5: Run tests to verify they pass**

Run: `go test -v ./internal/platform/version ./cmd/election-bot -count=1`
Expected: PASS

- [x] **Step 6: Commit**

```bash
git add internal/platform/version/ cmd/election-bot/ internal/platform/lifecycle/run.go
git commit -m "feat(version): add centralized version metadata and cli command"
```

---

### Task 2: Native Go Commit and Branch Validator (`cmd/dev-check`)

**Files:**
- Create: `cmd/dev-check/main.go`
- Test: `cmd/dev-check/main_test.go`

**Interfaces:**
- Produces: CLI `dev-check commit`, `dev-check branch`, `dev-check pr-title`, `dev-check release-config`
- Consumes: Standard library (`regexp`, `strings`, `os`, `fmt`)

- [x] **Step 1: Write the failing tests**

```go
// cmd/dev-check/main_test.go
package main

import "testing"

func TestValidateBranch(t *testing.T) {
	cases := []struct {
		branch string
		valid  bool
	}{
		{"feat/whatsapp-integration", true},
		{"fix/telegram-retry", true},
		{"ci/github-actions-release-pipeline", true},
		{"docs/update-architecture", true},
		{"refactor/outbox-scheduling", true},
		{"test/sqlite-recovery", true},
		{"build/docker-multiarch", true},
		{"hotfix/critical-crash", true},
		{"main", true},
		{"master", true},
		{"release-please--branches--main", true},
		{"dependabot/go_modules/pkg-1.0.0", true},
		{"feature/wrong-type", false},
		{"feat/Upper-Case", false},
		{"feat/", false},
		{"feat/has_underscore", false},
		{"feat/trailing-hyphen-", false},
		{"invalid", false},
	}
	for _, tc := range cases {
		err := validateBranch(tc.branch)
		if (err == nil) != tc.valid {
			t.Errorf("branch %q: valid=%t, got err=%v", tc.branch, tc.valid, err)
		}
	}
}

func TestValidateCommit(t *testing.T) {
	cases := []struct {
		msg   string
		valid bool
	}{
		{"feat(whatsapp): add persistent device sessions", true},
		{"fix(telegram): prevent duplicated milestone alerts", true},
		{"ci: add Docker publishing workflow", true},
		{"docs(readme): document container deployment", true},
		{"refactor(outbox): simplify delivery scheduling", true},
		{"test(sqlite): cover migration rollback", true},
		{"build(docker): enable multi-architecture builds", true},
		{"feat(api)!: change subscription contract", true},
		{"chore(main): release 0.1.0", true},
		{"Merge branch 'feat/whatsapp' into main", true},
		{"Merge pull request #42 from Diaszano/feat", true},
		{"random commit message", false},
		{"Feat: uppercase type", false},
		{"feat: Trailing period.", false},
		{"feat:empty", false},
		{"unknown(scope): unsupported type", false},
	}
	for _, tc := range cases {
		err := validateCommit(tc.msg)
		if (err == nil) != tc.valid {
			t.Errorf("commit %q: valid=%t, got err=%v", tc.msg, tc.valid, err)
		}
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test -v ./cmd/dev-check`
Expected: FAIL with `validateBranch` and `validateCommit` undefined.

- [x] **Step 3: Implement minimal `cmd/dev-check` CLI**

Implement `validateBranch`, `validateCommit`, `validatePRTitle`, and subcommands dispatch in `cmd/dev-check/main.go`.

- [x] **Step 4: Run test to verify it passes**

Run: `go test -v ./cmd/dev-check -count=1`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add cmd/dev-check/
git commit -m "feat(cli): add native dev-check for commit and branch validation"
```

---

### Task 3: Release Please Configuration and Bootstrap Verification

**Files:**
- Create: `release-please-config.json`, `.release-please-manifest.json`, `CHANGELOG.md`
- Test: `cmd/dev-check/release_test.go`
- Modify: `cmd/dev-check/main.go` (support `dev-check release-config`)

**Interfaces:**
- Produces: `dev-check release-config` verification command, Release Please configuration files.

- [x] **Step 1: Write failing test for release config validation**

In `cmd/dev-check/release_test.go`, test that `dev-check release-config` validates:
- `release-please-config.json` contains package `.` with `initial-version: "0.1.0"`.
- `.release-please-manifest.json` contains `".": "0.1.0"`.
- `CHANGELOG.md` exists and contains initial header.

- [x] **Step 2: Run test to verify it fails**

Run: `go test -v ./cmd/dev-check -run TestReleaseConfig`
Expected: FAIL (files missing).

- [x] **Step 3: Create Release Please configuration files**

Create:
- `release-please-config.json`
- `.release-please-manifest.json`
- `CHANGELOG.md`
- Implement `validateReleaseConfig` in `cmd/dev-check/main.go`.

- [x] **Step 4: Run test to verify it passes**

Run: `go test -v ./cmd/dev-check -run TestReleaseConfig -count=1`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add release-please-config.json .release-please-manifest.json CHANGELOG.md cmd/dev-check/
git commit -m "build(release): configure release-please and initial release bootstrap"
```

---

### Task 4: GitHub Actions CI Workflow (`.github/workflows/ci.yml`)

**Files:**
- Create: `.github/workflows/ci.yml`
- Test: `deployments/docker/ci_workflow_test.go` (validates YAML syntax and step structure)

**Interfaces:**
- Produces: GitHub Actions CI pipeline running `standards`, `go-quality`, `security`, and `docker-validation`.

- [x] **Step 1: Write workflow syntax and structure test**

Write a test checking that `.github/workflows/ci.yml` is valid YAML, specifies top-level `permissions: contents: read`, includes timeout on all jobs, pins actions with commit hashes, and contains required jobs (`standards`, `go-quality`, `security`, `docker-validation`).

- [x] **Step 2: Run test to verify it fails**

Run: `go test -v ./deployments/docker -run TestCIWorkflow`
Expected: FAIL (.github/workflows/ci.yml not found).

- [x] **Step 3: Implement `.github/workflows/ci.yml`**

Include:
- Triggers: `pull_request` to `main`, `push` to `main`, `workflow_dispatch`.
- Concurrency group with cancel-in-progress.
- Job `standards`: runs `go run ./cmd/dev-check branch` and `go run ./cmd/dev-check commit`.
- Job `go-quality`: Go setup (1.27.0), `go mod verify`, `go vet`, `golangci-lint`, `go test -count=1`, `go test -race`, `make simulate`, `CGO_ENABLED=0 go build`.
- Job `security`: `govulncheck`, `gitleaks`, dependency-review.
- Job `docker-validation`: build container, scan with Trivy (failing on CRITICAL), run smoke tests (`/health/ready`, `version` CLI).

- [x] **Step 4: Run test to verify it passes**

Run: `go test -v ./deployments/docker -run TestCIWorkflow -count=1`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add .github/workflows/ci.yml deployments/docker/ci_workflow_test.go
git commit -m "ci(actions): add comprehensive CI pipeline with quality and security gates"
```

---

### Task 5: Self-Contained Release & Publishing Workflow (`.github/workflows/release.yml`)

**Files:**
- Create: `.github/workflows/release.yml`
- Test: `deployments/docker/release_workflow_test.go`

**Interfaces:**
- Produces: Self-contained release workflow triggering Release Please and publishing GHCR images + binary assets + SBOM + provenance.

- [x] **Step 1: Write workflow structure test**

Test that `.github/workflows/release.yml` defines `release-please`, `verify-release-commit`, `publish-ghcr`, and `publish-release-assets`, requires commit SHA verification, configures multi-arch `linux/amd64,linux/arm64`, and supports `workflow_dispatch` recovery.

- [x] **Step 2: Run test to verify it fails**

Run: `go test -v ./deployments/docker -run TestReleaseWorkflow`
Expected: FAIL.

- [x] **Step 3: Implement `.github/workflows/release.yml`**

Include:
- `release-please` job running `googleapis/release-please-action` with `secrets.GITHUB_TOKEN`.
- `verify-release-commit` job checking commit status via GitHub API on the release SHA.
- `publish-ghcr` job building multi-arch with Buildx, scanning with Trivy, publishing to `ghcr.io/diaszano/election-bot`, generating SBOM and attestations.
- `publish-release-assets` job cross-compiling static binaries and uploading tarballs + checksums to GitHub Release.
- Idempotency guards checking for existing immutable tags before publishing.

- [x] **Step 4: Run test to verify it passes**

Run: `go test -v ./deployments/docker -run TestReleaseWorkflow -count=1`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add .github/workflows/release.yml deployments/docker/release_workflow_test.go
git commit -m "ci(release): add self-contained release and publishing workflow with ghcr multi-arch"
```

---

### Task 6: Repository Standards, Governance, and Developer Experience

**Files:**
- Create: `.editorconfig`, `.gitattributes`, `.github/CODEOWNERS`, `.github/dependabot.yml`, `.github/PULL_REQUEST_TEMPLATE.md`, `.github/ISSUE_TEMPLATE/bug_report.yml`, `.github/ISSUE_TEMPLATE/feature_request.yml`, `CONTRIBUTING.md`, `SECURITY.md`
- Modify: `Makefile`

**Interfaces:**
- Produces: Standard developer targets `make check`, `make ci`, `make version`, `make release-check`, Dependabot updates, and repository guidelines.

- [x] **Step 1: Write tests for developer commands**

In `cmd/dev-check/main_test.go` and Makefile checks, test that `make version` outputs valid version info and `make check` executes without error.

- [x] **Step 2: Run test to verify it fails**

Run: `make version` / `make check`
Expected: FAIL (targets not yet in Makefile).

- [x] **Step 3: Implement repository metadata and Makefile targets**

- Create `.editorconfig` (2 spaces YAML/MD, tabs Go, UTF-8, LF).
- Create `.gitattributes` (LF text, binary SQLite/media).
- Create `.github/CODEOWNERS` (`* @Diaszano`).
- Create `.github/dependabot.yml` (`gomod`, `github-actions`, `docker`, weekly schedule, `chore(deps)` prefix).
- Create `.github/PULL_REQUEST_TEMPLATE.md` and issue templates.
- Create `CONTRIBUTING.md` and `SECURITY.md`.
- Update `Makefile` with `check`, `ci`, `version`, `release-check`.

- [x] **Step 4: Run test to verify it passes**

Run: `make version && make check`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add .editorconfig .gitattributes .github/ CONTRIBUTING.md SECURITY.md Makefile
git commit -m "chore(repo): add repository standards, dependabot, and developer tooling"
```

---

### Task 7: Documentation, Verification Ledger, and Full Suite Validation

**Files:**
- Create: `docs/ci-cd.md`
- Modify: `docs/architecture.md`, `docs/observability.md`, `docs/validation.md`, `README.md`

**Interfaces:**
- Produces: Complete architectural CI/CD runbook, ruleset configuration guide, rollback manual, updated README, and verification ledger.

- [x] **Step 1: Check spec coverage and write `docs/ci-cd.md`**

Document branch strategy, Conventional Commits, SemVer policy, Release Please workflow, manual PR check approval, release failure recovery, GHCR image tags, GitHub Rulesets setup, and rollback instructions.

- [x] **Step 2: Update system documentation**

Update `README.md`, `docs/architecture.md`, and `docs/observability.md` to reflect version metadata, CI/CD pipelines, and OpenTelemetry `service.version`.

- [x] **Step 3: Run the complete verification set**

Execute:
- `go test ./...`
- `go test -race ./...`
- `go vet ./...`
- `make lint`
- `make check`
- `make build`
- `make simulate`
- `go mod verify`
- `CGO_ENABLED=0 go build -ldflags="-X 'github.com/Diaszano/election-bot/internal/platform/version.Version=v0.1.0' -X 'github.com/Diaszano/election-bot/internal/platform/version.Commit=testsha'" -o bin/election-bot ./cmd/election-bot`
- Test `/bin/election-bot version` and `/bin/election-bot version --json`.
- `docker build -t election-bot:latest .`
- Test container startup and health probe.
- Update `docs/validation.md` with all executed commands, outcomes, and unperformed remote limits.

- [x] **Step 4: Commit documentation**

```bash
git add docs/ README.md
git commit -m "docs(ci): record CI/CD pipeline architecture and verification ledger"
```

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-10-11-ci-cd-release-pipeline.md`. Two execution options:

1. **Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
