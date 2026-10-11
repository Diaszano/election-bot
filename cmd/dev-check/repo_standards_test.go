package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func findRepoRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to find repo root via git: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func checkFileNonEmpty(t *testing.T, repoRoot, relPath string) string {
	t.Helper()
	path := filepath.Join(repoRoot, relPath)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected file %q to exist: %v", relPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected file %q to be non-empty", relPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file %q: %v", relPath, err)
	}
	return string(data)
}

func TestRepoStandards(t *testing.T) {
	repoRoot := findRepoRoot(t)

	// 1. Verify required files exist and are non-empty
	requiredFiles := []string{
		".editorconfig",
		".gitattributes",
		".github/CODEOWNERS",
		".github/dependabot.yml",
		".github/PULL_REQUEST_TEMPLATE.md",
		".github/ISSUE_TEMPLATE/bug_report.yml",
		".github/ISSUE_TEMPLATE/feature_request.yml",
		"CONTRIBUTING.md",
		"SECURITY.md",
	}

	for _, relPath := range requiredFiles {
		t.Run("FileExists_"+relPath, func(t *testing.T) {
			checkFileNonEmpty(t, repoRoot, relPath)
		})
	}

	// 2. Verify .github/CODEOWNERS assigns @Diaszano
	t.Run("CODEOWNERS_Content", func(t *testing.T) {
		content := checkFileNonEmpty(t, repoRoot, ".github/CODEOWNERS")
		found := false
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || line == "" {
				continue
			}
			if strings.HasPrefix(line, "*") && strings.Contains(line, "@Diaszano") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected .github/CODEOWNERS to assign '*' to @Diaszano, got:\n%s", content)
		}
	})

	// 3. Verify .github/dependabot.yml configuration
	t.Run("Dependabot_Config", func(t *testing.T) {
		content := checkFileNonEmpty(t, repoRoot, ".github/dependabot.yml")

		// Parse dependabot YAML line by line to verify schema without external dependency
		scanner := bufio.NewScanner(strings.NewReader(content))
		hasVersion2 := false
		type updateItem struct {
			ecosystem string
			directory string
			interval  string
			prefix    string
		}
		var updates []updateItem
		var currentItem *updateItem
		inCommitMessage := false

		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}

			if strings.HasPrefix(trimmed, "version:") {
				val := strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
				if val == "2" {
					hasVersion2 = true
				}
			}

			if strings.HasPrefix(trimmed, "- package-ecosystem:") {
				if currentItem != nil {
					updates = append(updates, *currentItem)
				}
				eco := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- package-ecosystem:")), `"'`)
				currentItem = &updateItem{ecosystem: eco}
				inCommitMessage = false
				continue
			}

			if currentItem != nil {
				if strings.HasPrefix(trimmed, "package-ecosystem:") {
					currentItem.ecosystem = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "package-ecosystem:")), `"'`)
				} else if strings.HasPrefix(trimmed, "directory:") {
					currentItem.directory = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "directory:")), `"'`)
				} else if strings.HasPrefix(trimmed, "interval:") {
					currentItem.interval = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "interval:")), `"'`)
				} else if strings.HasPrefix(trimmed, "commit-message:") {
					inCommitMessage = true
				} else if inCommitMessage && strings.HasPrefix(trimmed, "prefix:") {
					currentItem.prefix = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "prefix:")), `"'`)
				}
			}
		}
		if currentItem != nil {
			updates = append(updates, *currentItem)
		}

		if !hasVersion2 {
			t.Errorf("expected dependabot version: 2")
		}

		expectedEcosystems := map[string]bool{
			"gomod":          false,
			"github-actions": false,
			"docker":         false,
		}

		for _, u := range updates {
			if _, exists := expectedEcosystems[u.ecosystem]; exists {
				expectedEcosystems[u.ecosystem] = true
			}
			if u.interval != "weekly" {
				t.Errorf("expected schedule interval 'weekly' for ecosystem %q, got %q", u.ecosystem, u.interval)
			}
			if u.prefix != "chore(deps)" {
				t.Errorf("expected commit-message.prefix 'chore(deps)' for ecosystem %q, got %q", u.ecosystem, u.prefix)
			}
		}

		for eco, found := range expectedEcosystems {
			if !found {
				t.Errorf("missing package-ecosystem %q in .github/dependabot.yml", eco)
			}
		}
	})

	// 4. Verify Makefile defines check, ci, version, and release-check targets
	t.Run("Makefile_Targets", func(t *testing.T) {
		content := checkFileNonEmpty(t, repoRoot, "Makefile")

		requiredTargets := []string{
			"check:",
			"ci:",
			"version:",
			"release-check:",
			"git-hooks:",
		}

		for _, target := range requiredTargets {
			if !strings.Contains(content, target) {
				t.Errorf("expected Makefile to define target %q", target)
			}
		}
	})
}
