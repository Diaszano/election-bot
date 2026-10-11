package version_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/Diaszano/election-bot/internal/platform/version"
)

func TestGetDefaults(t *testing.T) {
	info := version.Get()
	if info.Version == "" {
		t.Errorf("expected non-empty Version, got empty")
	}
	if info.GoVersion != runtime.Version() {
		t.Errorf("expected GoVersion %q, got %q", runtime.Version(), info.GoVersion)
	}
	expectedPlatform := runtime.GOOS + "/" + runtime.GOARCH
	if info.Platform != expectedPlatform {
		t.Errorf("expected Platform %q, got %q", expectedPlatform, info.Platform)
	}
}

func TestString(t *testing.T) {
	origVer, origCommit, origDate := version.Version, version.Commit, version.Date
	defer func() {
		version.Version, version.Commit, version.Date = origVer, origCommit, origDate
	}()

	version.Version = "v1.2.3"
	version.Commit = "abcdef123456"
	version.Date = "2026-10-11T12:00:00Z"

	s := version.String()
	expectedPrefix := "election-bot v1.2.3\n"
	if !strings.HasPrefix(s, expectedPrefix) {
		t.Errorf("expected string to start with %q, got %q", expectedPrefix, s)
	}
	if !strings.Contains(s, "Commit: abcdef123456\n") {
		t.Errorf("expected string to contain Commit, got %q", s)
	}
	if !strings.Contains(s, "Go: "+runtime.Version()+"\n") {
		t.Errorf("expected string to contain Go version, got %q", s)
	}
	expectedPlatform := "Platform: " + runtime.GOOS + "/" + runtime.GOARCH
	if !strings.Contains(s, expectedPlatform) {
		t.Errorf("expected string to contain Platform %q, got %q", expectedPlatform, s)
	}
}

func TestJSON(t *testing.T) {
	origVer, origCommit, origDate := version.Version, version.Commit, version.Date
	defer func() {
		version.Version, version.Commit, version.Date = origVer, origCommit, origDate
	}()

	version.Version = "v1.2.3"
	version.Commit = "abcdef123456"
	version.Date = "2026-10-11T12:00:00Z"

	data, err := version.JSON()
	if err != nil {
		t.Fatalf("unexpected error from JSON(): %v", err)
	}

	var parsed version.Info
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if parsed.Version != "v1.2.3" {
		t.Errorf("expected version v1.2.3, got %q", parsed.Version)
	}
	if parsed.Commit != "abcdef123456" {
		t.Errorf("expected commit abcdef123456, got %q", parsed.Commit)
	}
	if parsed.Date != "2026-10-11T12:00:00Z" {
		t.Errorf("expected date 2026-10-11T12:00:00Z, got %q", parsed.Date)
	}
	if parsed.GoVersion != runtime.Version() {
		t.Errorf("expected GoVersion %q, got %q", runtime.Version(), parsed.GoVersion)
	}
	if parsed.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Errorf("expected Platform %q, got %q", runtime.GOOS+"/"+runtime.GOARCH, parsed.Platform)
	}
}
