package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, dir, filename, content string) {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("failed to write test file %q: %v", path, err)
	}
}

func setupValidReleaseDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	validConfig := `{
  "$schema": "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json",
  "release-type": "go",
  "packages": {
    ".": {
      "package-name": "election-bot",
      "changelog-path": "CHANGELOG.md",
      "release-type": "go",
      "initial-version": "0.1.0",
      "bump-minor-pre-major": true,
      "bump-patch-for-minor-pre-major": false,
      "include-component-in-tag": false,
      "extra-files": []
    }
  }
}`

	validManifest := `{
  ".": "0.1.0"
}`

	validChangelog := `# Changelog

All notable changes to this project will be documented in this file.
`

	writeTestFile(t, dir, "release-please-config.json", validConfig)
	writeTestFile(t, dir, ".release-please-manifest.json", validManifest)
	writeTestFile(t, dir, "CHANGELOG.md", validChangelog)

	return dir
}

func TestValidateReleaseConfig_RepositoryRoot(t *testing.T) {
	// In the real repository root, validateReleaseConfig should check
	// the actual files in current directory.
	if err := validateReleaseConfig(); err != nil {
		t.Fatalf("validateReleaseConfig() on repository root failed: %v", err)
	}
}

func TestValidateReleaseConfig_Cases(t *testing.T) {
	t.Run("valid configuration passes", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		if err := validateReleaseConfig(dir); err != nil {
			t.Fatalf("expected valid config to pass, got: %v", err)
		}
	})

	t.Run("missing release-please-config.json fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		if err := os.Remove(filepath.Join(dir, "release-please-config.json")); err != nil {
			t.Fatal(err)
		}
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for missing release-please-config.json, got nil")
		}
	})

	t.Run("invalid json in release-please-config.json fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "release-please-config.json", "invalid json{")
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for malformed release-please-config.json, got nil")
		}
	})

	t.Run("missing packages map in release-please-config.json fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "release-please-config.json", `{"release-type": "go"}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for missing packages map, got nil")
		}
	})

	t.Run("missing root package . in release-please-config.json fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "release-please-config.json", `{"packages": {"subpkg": {}}}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for missing package ., got nil")
		}
	})

	t.Run("wrong package-name fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "release-please-config.json", `{
  "packages": {
    ".": {
      "package-name": "other-bot",
      "release-type": "go",
      "initial-version": "0.1.0",
      "bump-minor-pre-major": true,
      "include-component-in-tag": false
    }
  }
}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for wrong package-name, got nil")
		}
	})

	t.Run("wrong release-type fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "release-please-config.json", `{
  "packages": {
    ".": {
      "package-name": "election-bot",
      "release-type": "node",
      "initial-version": "0.1.0",
      "bump-minor-pre-major": true,
      "include-component-in-tag": false
    }
  }
}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for wrong release-type, got nil")
		}
	})

	t.Run("wrong initial-version fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "release-please-config.json", `{
  "packages": {
    ".": {
      "package-name": "election-bot",
      "release-type": "go",
      "initial-version": "1.0.0",
      "bump-minor-pre-major": true,
      "include-component-in-tag": false
    }
  }
}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for wrong initial-version, got nil")
		}
	})

	t.Run("bump-minor-pre-major false or missing fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "release-please-config.json", `{
  "packages": {
    ".": {
      "package-name": "election-bot",
      "release-type": "go",
      "initial-version": "0.1.0",
      "bump-minor-pre-major": false,
      "include-component-in-tag": false
    }
  }
}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for bump-minor-pre-major=false, got nil")
		}
	})

	t.Run("include-component-in-tag true fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "release-please-config.json", `{
  "packages": {
    ".": {
      "package-name": "election-bot",
      "release-type": "go",
      "initial-version": "0.1.0",
      "bump-minor-pre-major": true,
      "include-component-in-tag": true
    }
  }
}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for include-component-in-tag=true, got nil")
		}
	})

	t.Run("missing .release-please-manifest.json fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		if err := os.Remove(filepath.Join(dir, ".release-please-manifest.json")); err != nil {
			t.Fatal(err)
		}
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for missing manifest, got nil")
		}
	})

	t.Run("invalid json in .release-please-manifest.json fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, ".release-please-manifest.json", "invalid json{")
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for malformed manifest, got nil")
		}
	})

	t.Run("missing . key in manifest fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, ".release-please-manifest.json", `{"other": "0.1.0"}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for missing . in manifest, got nil")
		}
	})

	t.Run("manifest version mismatch fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, ".release-please-manifest.json", `{".": "0.2.0"}`)
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for manifest version mismatch, got nil")
		}
	})

	t.Run("missing CHANGELOG.md fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		if err := os.Remove(filepath.Join(dir, "CHANGELOG.md")); err != nil {
			t.Fatal(err)
		}
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for missing CHANGELOG.md, got nil")
		}
	})

	t.Run("empty CHANGELOG.md fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "CHANGELOG.md", "   \n\n  ")
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for empty CHANGELOG.md, got nil")
		}
	})

	t.Run("CHANGELOG.md without markdown header fails", func(t *testing.T) {
		dir := setupValidReleaseDir(t)
		writeTestFile(t, dir, "CHANGELOG.md", "Just some random plain text without any markdown header.")
		if err := validateReleaseConfig(dir); err == nil {
			t.Fatal("expected error for CHANGELOG.md without header, got nil")
		}
	})
}

func TestRunReleaseConfigCmd(t *testing.T) {
	dir := setupValidReleaseDir(t)
	if err := runReleaseConfigCmd([]string{dir}); err != nil {
		t.Fatalf("expected runReleaseConfigCmd to succeed on valid dir, got %v", err)
	}

	invalidDir := t.TempDir()
	if err := runReleaseConfigCmd([]string{invalidDir}); err == nil {
		t.Fatal("expected runReleaseConfigCmd to fail on empty dir, got nil")
	}
}
