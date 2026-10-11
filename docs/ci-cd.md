# CI/CD, Release Engineering, and Repository Operations

This document defines the continuous integration, continuous delivery (CD), automated release lifecycle, software supply chain security, and operational rollback procedures for `election-bot` (`github.com/Diaszano/election-bot`).

---

## 1. Pipeline Overview & Core Principles

The CI/CD pipeline for `election-bot` is designed around four foundational principles:

1. **Self-Contained & Zero External Credential Dependency**: CI and release pipelines run using native Go tooling and the standard GitHub Actions `GITHUB_TOKEN`. No external SaaS tokens, third-party bot accounts, or Personal Access Tokens (PATs) are required.
2. **Fail-Closed Verification Gates**: Every merge to `main` and every artifact release must pass exhaustive quality, security, and supply-chain gates. Releases verify the exact commit SHA that passed all mandatory CI checks before any build or publishing step begins.
3. **Reproducibility & Immutable Artifacts**: Released container images and binaries are cryptographically attested, indexed with SPDX SBOMs, and tagged with immutable SemVer identifiers. Once pushed, version tags are never overwritten.
4. **Strict Safety in Pre-Production**: Real official TSE presidential runoff endpoints remain gated (`DATA_SOURCE_MODE=simulation` enforced in CI). Telemetry, Telegram, and WhatsApp credentials are never configured or tested with live network accounts in CI.

```mermaid
flowchart TD
    Feature[Feature branch: type/description] --> PR[Pull Request to main]
    PR --> CI[GitHub Actions CI: ci.yml]
    subgraph CI Gates
        CI --> Standards[standards: dev-check branch/commits]
        CI --> Quality[go-quality: test, race, lint, simulate, static build]
        CI --> Security[security: govulncheck, gitleaks, dependency-review]
        CI --> DockerVal[docker-validation: multi-arch build, Trivy scan, smoke test]
    end
    Standards & Quality & Security & DockerVal --> Approved[Maintainer Review & Merge]
    Approved --> Main[main branch]
    Main --> ReleaseWorkflow[Release Workflow: release.yml]
    subgraph CD Pipeline
        ReleaseWorkflow --> ReleasePlease[release-please: open/update Release PR or tag release]
        ReleasePlease --> MergedRelease{Release Merged & Tagged?}
        MergedRelease -- No --> AwaitMerge[Release PR chore(main) pending approval]
        MergedRelease -- Yes --> VerifySHA[verify-release-commit: exact SHA checks verified]
        VerifySHA --> PublishGHCR[publish-ghcr: multi-arch Docker, Trivy scan, SBOM, attestation]
        VerifySHA --> PublishAssets[publish-release-assets: cross-compile binaries, checksums, SBOM]
    end
```

---

## 2. Branch Strategy

The repository follows a **Trunk-Based Development** model with short-lived feature branches and `main` as the source of truth.

### 2.1 Branch Naming Conventions

All branches targeting `main` must adhere to the naming format:
```text
<type>/<kebab-case-description>
```

- **Allowed Types**:
  - `feat/`: New features or enhancements (e.g. `feat/outbox-retry-tuning`)
  - `fix/`: Bug fixes (e.g. `fix/sqlite-wal-checkpoint`)
  - `docs/`: Documentation updates (e.g. `docs/update-runbook`)
  - `style/`: Code formatting, white space, or non-functional styling
  - `refactor/`: Code changes that neither fix a bug nor add a feature
  - `perf/`: Performance improvements
  - `test/`: Adding or correcting tests
  - `build/`: Build system, Go toolchain, or external dependency changes
  - `ci/`: GitHub Actions workflows and CI scripts
  - `chore/`: Routine maintenance and repository housekeeping
  - `hotfix/`: Urgent production fixes directly targeting the current release

- **Allowed Characters in Description**: Lowercase alphanumeric characters (`[a-z0-9]`) and single hyphens (`-`).
- **Forbidden**: Uppercase characters, underscores, consecutive hyphens (`--`), leading or trailing hyphens.
- **Exempt Branches**: `main`, `master`, Release Please branches (`release-please--*`), and Dependabot branches (`dependabot/*`).

Branch naming is automatically enforced locally via `make check` and in CI by the `standards` job using the native `cmd/dev-check branch` command.

---

## 3. Commit Message Standards

The repository strictly enforces **Conventional Commits 1.0.0**.

### 3.1 Format

```text
<type>[optional scope][!]: <description>

[optional body]

[optional footer(s)]
```

- **Header Rules**:
  - `type`: Must be one of `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`, `ci`, `chore`, `revert`.
  - `scope` (optional): Alphanumeric string enclosed in parentheses, e.g. `feat(whatsapp): ...` or `fix(sqlite): ...`.
  - `!`: Breaking change indicator placed before the colon, e.g. `feat(api)!: change payload schema`.
  - `description`: Lowercase, imperative sentence, no trailing period or punctuation.
  - Length: Maximum 100 characters for the header.
- **Body & Footer Rules**:
  - A blank line MUST separate the header from the body or footer.
  - Breaking changes must either include `!` in the header or contain a footer beginning with `BREAKING CHANGE: <explanation>`.
  - Multiple footers are supported (e.g. `Co-authored-by: ...`, `Signed-off-by: ...`).

### 3.2 Automated Enforcement & Exemptions

Commit validation is performed by the zero-dependency Go utility `cmd/dev-check`:
- **Pull Request Range Validation**: In CI, the `standards` job executes `cmd/dev-check pr-commits --base <base-sha> --head <head-sha>` to validate **all newly introduced commits** in the pull request range, ensuring no non-compliant commits can be merged via squash or merge commits.
- **Single Commit & Hook Validation**: Developers can validate individual commit messages via `cmd/dev-check commit --msg "..."` or install the automated git hook via `make git-hooks`.
- **Exemptions**:
  - Non-fast-forward merge commits (`Merge branch ...`, `Merge pull request ...`, `Merge made by the ...`)
  - Automated Release Please commits (`chore(main): release ...`)
  - Automated Dependabot commits (`chore(deps): ...`, `chore(deps-dev): ...`)
  - Historical ancestor commits (commits predating the PR branch point are not retroactively evaluated)

---

## 4. Semantic Versioning 2.0.0 Policy

The project implements **Semantic Versioning 2.0.0** (`vMAJOR.MINOR.PATCH`).

### 4.1 Increment Rules

Version increments are computed automatically by Release Please based on Conventional Commits:

| Commit Type | SemVer Impact (Pre-1.0: `0.y.z`) | SemVer Impact (Post-1.0: `>= 1.0.0`) |
|---|---|---|
| `fix:`, `perf:` | **PATCH** (`0.1.0` -> `0.1.1`) | **PATCH** (`1.0.0` -> `1.0.1`) |
| `feat:` | **MINOR** (`0.1.0` -> `0.2.0`) | **MINOR** (`1.0.0` -> `1.1.0`) |
| `!` or `BREAKING CHANGE:` | **MINOR** (`0.1.0` -> `0.2.0`) | **MAJOR** (`1.0.0` -> `2.0.0`) |
| `docs:`, `style:`, `refactor:`, `test:`, `build:`, `ci:`, `chore:` | No release triggered | No release triggered |

*Note on Pre-1.0 Behavior*: In accordance with SemVer 2.0.0 Clause 4, initial development (`0.y.z`) allows public APIs to change at any time. Consequently, breaking changes increment the MINOR version, and `bump-minor-pre-major: true` is configured in `release-please-config.json`.

### 4.2 Initial Release Bootstrap (`v0.1.0`)

The initial release of `election-bot` is deterministically pinned to **`v0.1.0`**:
- Configuration in `release-please-config.json`:
  - `"package-name": "election-bot"`
  - `"initial-version": "0.1.0"`
  - `"bump-minor-pre-major": true`
  - `"bump-patch-for-minor-pre-major": false`
- Configuration in `.release-please-manifest.json`:
  - `".": "0.1.0"`
- Release Please dry-run validation is verified in CI and unit tests via `cmd/dev-check release-config`.

---

## 5. Continuous Integration (CI) Pipeline

The CI workflow is defined in [`.github/workflows/ci.yml`](../.github/workflows/ci.yml) and runs on pull requests targeting `main`, pushes to `main`, and manual `workflow_dispatch`.

### 5.1 Mandatory CI Jobs

All four jobs must succeed for a PR to be mergeable:

1. **`standards`**:
   - Checks out full git history (`fetch-depth: 0`).
   - Validates branch name using `cmd/dev-check branch`.
   - Validates PR title using `cmd/dev-check pr-title`.
   - Validates all PR commits using `cmd/dev-check commit`.
   - Validates `release-please-config.json` and `.release-please-manifest.json` using `cmd/dev-check release-config`.
   - Validates GitHub Actions workflow YAML formatting.

2. **`go-quality`**:
   - Go toolchain: Go 1.27.0.
   - Dependency integrity: `go mod verify`.
   - Static analysis: `go vet ./...`.
   - Linting: `golangci-lint` (pinned to v2.13.2) with strict zero-warning policy.
   - Unit tests: `go test -v -count=1 ./...`.
   - Race detection: `go test -race ./...`.
   - Offline recovery scenario: `make simulate` (`go run ./cmd/election-bot --simulate`).
   - Pure-Go build verification: `CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/election-bot`.

3. **`security`**:
   - `govulncheck`: Analyzes Go codebase and module graph for known vulnerabilities.
   - `gitleaks`: Scans commits and working tree for leaked API keys, tokens, or private secrets.
   - `dependency-review-action`: Validates introduced dependencies on PRs against known CVEs and license compatibility.

4. **`docker-validation`**:
   - Sets up Docker Buildx with QEMU support.
   - Multi-arch build verification: builds local test container for `linux/amd64`.
   - Trivy Vulnerability Gate: Scans container filesystem with `aquasecurity/trivy-action`, failing on unexempted `CRITICAL` vulnerabilities.
   - Container Smoke Test:
     - Runs container as UID 65532 (non-root distroless runtime).
     - Tests `/health/ready` probe under `DATA_SOURCE_MODE=simulation`.
     - Validates version introspection CLI: `election-bot version`.
     - Confirms clean process termination under SIGTERM.

---

## 6. Release & Publishing (CD) Pipeline

The release workflow is defined in [`.github/workflows/release.yml`](../.github/workflows/release.yml) and runs on pushes to `main` and `workflow_dispatch`.

### 6.1 Release Safety & Pre-Tag CI Verification Gate

To ensure that unverified commits are NEVER tagged or released, the release workflow enforces a pre-release gate:

1. **Pre-Release Verification (`verify-release-commit`)**:
   - Executes FIRST upon push to `main` (or via `workflow_dispatch` with `recovery_tag`).
   - Resolves the target commit SHA (`github.sha` on push to `main`).
   - Queries the GitHub Checks and Status APIs, polling until all four mandatory CI jobs (`standards`, `go-quality`, `security`, `docker-validation`) have completed with `success`.
   - If any check fails, times out, or is missing, the workflow terminates immediately with an error.

2. **Release Please Execution (`release-please`)**:
   - Depends directly on `verify-release-commit` (`needs: verify-release-commit`).
   - Runs ONLY AFTER all mandatory CI checks for the exact commit SHA have succeeded.
   - When conventional commits merge into `main`, Release Please computes the next version, updates `CHANGELOG.md` and `.release-please-manifest.json`, and opens or updates a Release PR titled `chore(main): release X.Y.Z`.
   - When a Release PR itself is merged into `main`, Release Please detects the merge commit, tags the repository (e.g. `v0.1.0`), creates the GitHub Release, and outputs `release_created: true`, `tag_name: v0.1.0`, and `sha`.
   - Because `release-please` runs after `verify-release-commit`, **no Git tag or GitHub Release can be created unless the release commit has completely passed all CI checks**.

3. **Downstream Publishing**:
   - Jobs `publish-ghcr` and `publish-release-assets` depend on both `verify-release-commit` and `release-please`.
   - They execute only when `release_created: true` or when operating in manual recovery mode.

### 6.3 Docker Publishing to GHCR

Job `publish-ghcr` publishes multi-architecture images to GitHub Container Registry:

- **Registry**: `ghcr.io/diaszano/election-bot`
- **Architectures**: `linux/amd64`, `linux/arm64`
- **Tagging Strategy**:
  - **Immutable Tags**: `v0.1.0` and `0.1.0` (permanent snapshots, never mutated).
  - **Minor Series Alias**: `0.1` (tracks latest patch in minor series).
  - **Major Series Alias**: `1` (for releases `>= 1.0.0`).
  - **`latest` Tag Policy**: Strictly reserved for releases `>= 1.0.0`. Pre-1.0 releases (`0.y.z`) NEVER receive the `latest` tag.
- **Trivy Multi-Architecture Vulnerability Gate**:
  - Both target architectures (`linux/amd64` and `linux/arm64`) are independently built locally as candidate images (`election-bot:candidate-amd64` and `election-bot:candidate-arm64`) and scanned with Trivy.
  - Severity policy: `severity: CRITICAL` with `exit-code: 1`. Any unignored critical finding on either architecture immediately terminates the workflow and prevents publishing.
  - Artifact identity: Both candidates are compiled from the exact verified commit SHA (`commit_sha`) using pinned base images (`golang:1.27.0-alpine3.21` and `gcr.io/distroless/static-debian12:nonroot`). The Buildx layer cache ensures that the published multi-arch manifest list references the identical compiled artifacts that underwent security scanning.
- **Software Bill of Materials (SBOM)**: Generated in SPDX JSON format using `anchore/sbom-action` and pushed to GHCR alongside the image.
- **Cryptographic Attestation**: Build provenance is generated and signed using `actions/attest-build-provenance` via GitHub's Sigstore integration.

### 6.4 Release Asset Compilation

Job `publish-release-assets` builds static cross-compiled archives:
- Static binaries: `CGO_ENABLED=0` for `linux/amd64` and `linux/arm64`.
- Linker metadata:
  - `-X 'github.com/Diaszano/election-bot/internal/platform/version.Version=vX.Y.Z'`
  - `-X 'github.com/Diaszano/election-bot/internal/platform/version.Commit=<sha>'`
  - `-X 'github.com/Diaszano/election-bot/internal/platform/version.Date=<rfc3339-timestamp>'`
- Archive creation: `.tar.gz` packages containing binary, `README.md`, and `CHANGELOG.md`.
- SHA-256 Checksums: Generated into `checksums.txt`.
- Assets attached to GitHub Release: Tarballs, checksums, and SPDX SBOM.

---

## 7. Release Recovery & Idempotency Runbook

If release creation succeeds (Git tag and GitHub Release created) but downstream publishing jobs fail due to transient network errors or registry rate limits:

### 7.1 Recovery Principles

1. **NEVER delete or force-move a Git tag**: Mutating tags violates immutability guarantees and breaks downstream package consumers.
2. **Idempotency Guard**: Re-running the release workflow checks if target images or assets already exist. If an existing image digest matches, it is preserved without error.

### 7.2 Manual Recovery via `workflow_dispatch`

The `.github/workflows/release.yml` workflow exposes a manual recovery entrypoint:

1. Navigate to **Actions** -> **Release & Publish Artifacts** -> **Run workflow**.
2. Supply recovery inputs:
   - `recovery_tag`: The existing target SemVer tag to re-publish or recover (e.g. `v0.1.0`).
3. The workflow validates:
   - Resolves the exact commit SHA associated with `recovery_tag`.
   - Verifies that mandatory CI checks passed for that SHA.
   - Missing assets (Docker images, binary tarballs, SBOMs, attestations) are built, verified, and uploaded.

---

## 8. GitHub Rulesets & Branch Protection

To guarantee repository integrity, security, and traceability, the repository uses GitHub Rulesets.

### 8.1 Main Branch Protection Ruleset (`main-protection`)

- **Ruleset Name**: `main-protection`
- **Enforcement Status**: `Active`
- **Target Branches**: `Include default branch` (`refs/heads/main`)
- **Protection Rules**:
  - **Require a pull request before merging**: Enabled.
    - **Review Policy**:
      - **Solo Maintainer Mode (Default)**:
        - Required approvals: `0` (prevents repository deadlock since the author `@Diaszano` cannot approve their own pull request).
        - Require review from Code Owners: Disabled for merge gating, while preserving `.github/CODEOWNERS` for ownership and review visibility.
        - Dismiss stale pull request approvals: Enabled.
        - Require conversation resolution: Enabled.
        - Strict CI enforcement: All 4 mandatory status checks must pass before merging. No routine bypass of CI checks.
      - **Collaborative Mode (When multiple maintainers exist)**:
        - Required approvals: `1` (or more).
        - Require review from Code Owners: Enabled (`.github/CODEOWNERS`).
        - Dismiss stale pull request approvals: Enabled.
        - Require conversation resolution: Enabled.
  - **Require status checks to pass before merging**: Enabled.
    - Require branches to be up to date before merging: Enabled.
    - Mandatory status checks (exact context names produced by `.github/workflows/ci.yml`):
      - `standards` (Governance, branch naming, PR title, and PR commit range Conventional Commits)
      - `go-quality` (Unit tests, race detector, go vet, golangci-lint, offline simulation)
      - `security` (govulncheck, gitleaks, dependency review)
      - `docker-validation` (Container build, non-root smoke test, and Trivy scan)
  - **Block force pushes**: Enabled (strict).
  - **Restrict deletions**: Enabled (strict).
  - **Require linear history**: **DISABLED (Strictly Disabled)**.
    - *Rationale*: The project enforces a non-fast-forward merge policy (`git merge --no-ff`) for release and feature branch merges to maintain full historical traceability. Enabling "Require linear history" would break this established integration workflow.
  - **Allowed Merge Methods**: Allow Merge Commits (`git merge --no-ff`).
  - **Bypass Actors**: None (or strictly restricted to Repository Administrators for disaster recovery).

### 8.2 Release Tag Immutability Ruleset (`release-tag-immutability`)

To guarantee strict Semantic Versioning 2.0.0 immutability:

- **Ruleset Name**: `release-tag-immutability`
- **Enforcement Status**: `Active`
- **Target Refs**: `Include: refs/tags/v*`
- **Protection Rules**:
  - **Restrict updates**: Enabled (prevents moving existing tags to different commits).
  - **Restrict deletions**: Enabled (prevents deleting published version tags).
  - **Restrict creations**: **DISABLED**.
    - *Critical Distinction*: Leaving "Restrict creations" disabled allows authorized release workflows (e.g. `release-please` running with `GITHUB_TOKEN` and `contents: write`) to legitimately create new version tags (such as `v0.1.0`), while permanently forbidding anyone from modifying or deleting existing tags.

---

## 9. Operational Rollback Manual

In the event of an operational regression, performance degradation, or service disruption in production, follow this rollback manual:

### 9.1 Container Deployment Rollback (Immediate Mitigation)

The primary production deployment runs Docker containers. Rollback is executed by switching the container image reference to the previous stable release:

1. **Identify Previous Stable Tag or Digest**:
   Check the deployment manifest or release history for the previous verified version:
   ```sh
   # Example: rolling back from v0.1.1 to v0.1.0
   PREVIOUS_IMAGE="ghcr.io/diaszano/election-bot:v0.1.0"
   # Or using immutable digest (recommended):
   PREVIOUS_DIGEST="ghcr.io/diaszano/election-bot@sha256:<known-good-digest>"
   ```
2. **Update Deployment Configuration**:
   Update `compose.yaml` or container orchestration manifest to reference `$PREVIOUS_IMAGE` or `$PREVIOUS_DIGEST`.
3. **Restart Service**:
   ```sh
   docker compose down election-bot
   docker compose up -d election-bot
   ```
4. **Verify Health and Synchronization**:
   ```sh
   curl -f http://127.0.0.1:8080/health/ready
   # Check logs for clean initialization
   docker compose logs -f election-bot
   ```

### 9.2 Git Tag Retraction Policy

1. **Git Tags Are Immutable**: Never delete or rewrite an already published Git tag (`v0.1.0`). Downstream Go module caches, container registries, and operator automation rely on tag immutability.
2. **Retraction Process**:
   - If a release contains a critical flaw, mark the GitHub Release as "Pre-release" or edit the release notes prominently warning operators:
     ```markdown
     > [!WARNING]
     > **RETRACTED RELEASE**: Version vX.Y.Z contains a critical bug (Issue #123).
     > Please use vX.Y.(Z-1) or upgrade to vX.Y.(Z+1).
     ```
   - In Go modules, if applicable, add a `retract` directive to `go.mod` in the subsequent release:
     ```go
     // go.mod
     retract v0.1.1 // Critical outbox state regression
     ```

### 9.3 Emergency Hotfix Procedure

When a production defect requires an immediate code fix:

1. **Create Hotfix Branch**:
   Branch directly from `main` (or the release tag if `main` has unreleased work):
   ```sh
   git checkout -b hotfix/fix-outbox-leak main
   ```
2. **Implement Minimal Fix**:
   - Follow YAGNI/KISS: Make the minimal necessary change to resolve the defect.
   - Include regression tests verifying the failure and the fix.
3. **Commit with Conventional Commit Header**:
   ```sh
   git commit -m "fix(outbox): resolve connection leak during network stall"
   ```
4. **Open Hotfix PR to `main`**:
   - CI runs all mandatory checks (`standards`, `go-quality`, `security`, `docker-validation`).
   - Review and merge the hotfix PR.
5. **Release Please Automatically Bumps PATCH**:
   - Release Please opens `chore(main): release 0.1.1`.
   - Maintainer approves and merges the release PR.
   - Downstream CD publishes `v0.1.1` Docker images and binaries.
6. **Deploy Hotfix**:
   Deploy `ghcr.io/diaszano/election-bot:v0.1.1` to production.

---

## 10. Developer Quick Reference

| Command | Purpose |
|---|---|
| `make check` | Runs native branch, commit, and release configuration checks via `cmd/dev-check` |
| `make ci` | Runs full local CI sequence (`go mod verify`, `go vet`, `make lint`, `test-race`, `simulate`, static build) |
| `make version` | Inspects current version metadata via `election-bot version` |
| `make release-check` | Validates `release-please` config and manifest consistency |
| `make git-hooks` | Installs local `commit-msg` hook for automatic Conventional Commit validation |
| `election-bot version` | CLI introspection displaying version, commit, Go version, and platform |
| `election-bot version --json` | CLI introspection outputting structured JSON metadata |

---

## 11. Remote Activation Runbook (Step-by-Step)

This runbook outlines the required sequence to safely activate GitHub Actions, Rulesets, Release Please, and GHCR upon receiving operator authorization to push to remote GitHub.

### Phase A: Local Preparation
1. Confirm the working tree on `main` is completely clean:
   ```sh
   git status
   ```
2. Verify that local verification checks pass:
   ```sh
   make check
   make ci
   make release-check
   ```
3. Confirm that the initial planned version in `.release-please-manifest.json` is `0.1.0` and no local Git tag exists:
   ```sh
   git tag -l "v*"
   ```

### Phase B: First Push (Requires Operator Authorization)
1. Execute the initial push of `main` to GitHub:
   ```sh
   git push origin main
   ```
   *(Note: Do not execute without explicit user authorization).*
2. Navigate to **Actions** in GitHub web UI and verify:
   - `CI` workflow starts and completes successfully on `main` across all four jobs: `standards`, `go-quality`, `security`, and `docker-validation`.
   - `Release & Publishing` workflow starts, validates CI completion on the commit SHA, and evaluates conventional commits.
   - Initial Release Please run opens the first Release PR: `chore(main): release 0.1.0`.

### Phase C: Repository & Ruleset Activation
1. **Configure Actions Permissions**:
   - Navigate to **Settings** -> **Actions** -> **General**.
   - Under **Workflow permissions**, select **Read repository contents and packages permissions**.
   - Check **Allow GitHub Actions to create and approve pull requests** (required for Release Please to create PRs using `GITHUB_TOKEN`).
2. **Activate Main Branch Ruleset (`main-protection`)**:
   - Navigate to **Settings** -> **Rules** -> **Rulesets** -> **New ruleset**.
   - Configure as documented in Section 8.1 with **Solo Maintainer Mode**:
     - Ruleset name: `main-protection`
     - Enforcement: `Active`
     - Target: `refs/heads/main`
     - Require pull request: Enabled (Approvals: `0`)
     - Require status checks: Enabled (`standards`, `go-quality`, `security`, `docker-validation`)
     - Require branches up to date: Enabled
     - Require linear history: **DISABLED**
     - Allowed merge methods: Allow merge commits (`git merge --no-ff`)
3. **Activate Release Tag Ruleset (`release-tag-immutability`)**:
   - Create ruleset targeting `refs/tags/v*`:
     - Ruleset name: `release-tag-immutability`
     - Enforcement: `Active`
     - Target refs: `Include: refs/tags/v*`
     - Restrict updates: Enabled
     - Restrict deletions: Enabled
     - Restrict creations: **DISABLED** (allows `release-please` to legitimately create tags)

### Phase D: Test Pull Requests (Controlled Verification)
1. Create a feature branch: `git checkout -b test/ci-activation main`.
2. Add a minor documentation fix, commit with conventional header: `docs(readme): verify ci activation`.
3. Open a Pull Request to `main`.
4. Verify that:
   - `standards` validates branch name, PR title, and all PR commit messages.
   - `go-quality`, `security`, and `docker-validation` execute and pass.
   - No registry publication occurs.
5. Merge the PR using `git merge --no-ff`.

### Phase E: Release Please PR Verification
1. Inspect the open Release PR: `chore(main): release 0.1.0`.
2. As documented for PRs created by `GITHUB_TOKEN`, click **Approve and run workflows** in the GitHub UI to trigger CI verification on the Release PR.
3. Verify that `standards`, `go-quality`, `security`, and `docker-validation` pass on the Release PR.
4. Review the generated `CHANGELOG.md` entry for `v0.1.0`.

### Phase F: First Official Release Publication (`v0.1.0`)
1. Merge the Release PR into `main`.
2. Observe `Release & Publishing` workflow:
   - Job `verify-release-commit` polls and confirms that all 4 CI checks passed on the release commit SHA.
   - Job `release-please` creates Git tag `v0.1.0` and publishes the GitHub Release.
   - Job `publish-ghcr` builds and scans `linux/amd64` and `linux/arm64` candidate images with Trivy, pushes the multi-arch image to GHCR, generates the SPDX SBOM, and emits Sigstore provenance attestations.
   - Job `publish-release-assets` compiles cross-platform static binaries and uploads `.tar.gz` packages with checksums to the release.
3. Verify the published container image:
   ```sh
   docker pull ghcr.io/diaszano/election-bot:v0.1.0
   docker run --rm ghcr.io/diaszano/election-bot:v0.1.0 version
   ```
