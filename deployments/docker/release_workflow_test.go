package docker

import (
	"regexp"
	"strings"
	"testing"
)

func TestReleaseWorkflow(t *testing.T) {
	content := readRootFile(t, ".github/workflows/release.yml")

	topLevels, jobs, err := parseWorkflow(content)
	if err != nil {
		t.Fatalf("invalid YAML syntax in .github/workflows/release.yml: %v", err)
	}

	// 1. Triggers: push to main, workflow_dispatch with optional recovery_tag input
	onLines, ok := topLevels["on"]
	if !ok {
		t.Fatalf(".github/workflows/release.yml missing 'on' trigger section")
	}
	onContent := strings.Join(onLines, "\n")

	if !strings.Contains(onContent, "push:") || !hasBranch(onLines, "push", "main") {
		t.Errorf("expected 'push' trigger targeting 'main'")
	}
	if !strings.Contains(onContent, "workflow_dispatch:") {
		t.Errorf("expected 'workflow_dispatch' trigger")
	}
	if !strings.Contains(onContent, "recovery_tag:") {
		t.Errorf("expected 'workflow_dispatch' trigger to include 'recovery_tag' input")
	}

	// 2. Top-level permissions: contents: read
	permLines, ok := topLevels["permissions"]
	if !ok {
		t.Fatalf(".github/workflows/release.yml missing top-level 'permissions' section")
	}
	permContent := strings.Join(permLines, " ")
	if !strings.Contains(permContent, "contents: read") {
		t.Errorf("expected top-level permissions to contain 'contents: read', got %q", permContent)
	}

	// 3. Concurrency group configured with cancel-in-progress: false
	concurLines, ok := topLevels["concurrency"]
	if !ok {
		t.Fatalf(".github/workflows/release.yml missing top-level 'concurrency' section")
	}
	concurContent := strings.Join(concurLines, "\n")
	if !strings.Contains(concurContent, "group:") {
		t.Errorf("expected concurrency group to be configured")
	}
	if !strings.Contains(concurContent, "cancel-in-progress: false") {
		t.Errorf("expected concurrency to specify cancel-in-progress: false to avoid terminating releases, got: %s", concurContent)
	}

	// 4. Required jobs: release-please, verify-release-commit, publish-ghcr, publish-release-assets
	requiredJobs := []string{"release-please", "verify-release-commit", "publish-ghcr", "publish-release-assets"}
	for _, req := range requiredJobs {
		job, ok := jobs[req]
		if !ok {
			t.Errorf("missing required job: %q", req)
			continue
		}

		// 5. Explicit timeout-minutes on all jobs
		if job.timeoutMinutes <= 0 {
			t.Errorf("job %q missing explicit timeout-minutes (> 0)", req)
		}
	}

	// 6. All actions pinned to 40-character commit SHAs
	actionShaRegex := regexp.MustCompile(`^[a-zA-Z0-9_\-\./]+@[0-9a-f]{40}$`)
	for jobName, job := range jobs {
		for _, step := range job.steps {
			if step.uses == "" {
				continue
			}
			actionRef := strings.TrimSpace(strings.Split(step.uses, "#")[0])
			if !actionShaRegex.MatchString(actionRef) {
				t.Errorf("job %q step %q action %q is not pinned to a 40-character commit SHA",
					jobName, step.name, actionRef)
			}
		}
	}

	// 7. Verify Job 'release-please'
	if job, ok := jobs["release-please"]; ok {
		jobStr := strings.Join(job.lines, "\n")
		if !strings.Contains(jobStr, "googleapis/release-please-action@7987652d64d4581673a76033cf5d487102541031") {
			t.Errorf("release-please job must use googleapis/release-please-action@7987652d64d4581673a76033cf5d487102541031")
		}
		if !strings.Contains(jobStr, "secrets.GITHUB_TOKEN") {
			t.Errorf("release-please job must use secrets.GITHUB_TOKEN")
		}
		// Crucial safety gate: release-please MUST depend on verify-release-commit
		if !strings.Contains(jobStr, "needs: verify-release-commit") && !strings.Contains(jobStr, "- verify-release-commit") {
			t.Errorf("release-please must depend on verify-release-commit so that CI checks pass before creating tags/releases")
		}
	}

	// 8. Verify Job 'verify-release-commit'
	if job, ok := jobs["verify-release-commit"]; ok {
		jobStr := strings.Join(job.lines, "\n")
		// Must verify the 4 CI checks
		for _, check := range []string{"standards", "go-quality", "security", "docker-validation"} {
			if !strings.Contains(jobStr, check) {
				t.Errorf("verify-release-commit must check required CI status for %q", check)
			}
		}
	}

	// 9. Verify Job 'publish-ghcr'
	if job, ok := jobs["publish-ghcr"]; ok {
		jobStr := strings.Join(job.lines, "\n")
		if !strings.Contains(jobStr, "needs:") || !strings.Contains(jobStr, "verify-release-commit") || !strings.Contains(jobStr, "release-please") {
			t.Errorf("publish-ghcr must depend on verify-release-commit and release-please")
		}
		if !strings.Contains(jobStr, "ghcr.io") {
			t.Errorf("publish-ghcr must log in and push to ghcr.io")
		}
		if !strings.Contains(jobStr, "linux/amd64,linux/arm64") {
			t.Errorf("publish-ghcr must target multi-arch linux/amd64,linux/arm64")
		}
		if !strings.Contains(jobStr, "aquasecurity/trivy-action") || !strings.Contains(jobStr, "CRITICAL") {
			t.Errorf("publish-ghcr must scan with Trivy for CRITICAL vulnerabilities")
		}
		if !strings.Contains(jobStr, "candidate-amd64") || !strings.Contains(jobStr, "candidate-arm64") {
			t.Errorf("publish-ghcr must build and scan candidate images for both linux/amd64 and linux/arm64")
		}
		trivyIdx := strings.Index(jobStr, "aquasecurity/trivy-action")
		pushIdx := strings.Index(jobStr, "push: true")
		if trivyIdx == -1 || pushIdx == -1 || trivyIdx >= pushIdx {
			t.Errorf("publish-ghcr must execute Trivy vulnerability scan before pushing images to GHCR")
		}
		if !strings.Contains(jobStr, "anchore/sbom-action") {
			t.Errorf("publish-ghcr must generate SBOM via anchore/sbom-action")
		}
		if !strings.Contains(jobStr, "actions/attest-build-provenance") {
			t.Errorf("publish-ghcr must generate provenance via actions/attest-build-provenance")
		}
	}

	// 10. Verify Job 'publish-release-assets'
	if job, ok := jobs["publish-release-assets"]; ok {
		jobStr := strings.Join(job.lines, "\n")
		if !strings.Contains(jobStr, "needs:") || !strings.Contains(jobStr, "verify-release-commit") || !strings.Contains(jobStr, "release-please") {
			t.Errorf("publish-release-assets must depend on verify-release-commit and release-please")
		}
		if !strings.Contains(jobStr, "linux/amd64") || !strings.Contains(jobStr, "linux/arm64") {
			t.Errorf("publish-release-assets must compile for linux/amd64 and linux/arm64")
		}
		if !strings.Contains(jobStr, ".tar.gz") {
			t.Errorf("publish-release-assets must package .tar.gz archives")
		}
		if !strings.Contains(jobStr, "sha256") || !strings.Contains(jobStr, "checksums.txt") {
			t.Errorf("publish-release-assets must generate sha256 checksums file")
		}
	}
}
