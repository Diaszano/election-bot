package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var allowedBranchTypes = map[string]bool{
	"feat":     true,
	"fix":      true,
	"docs":     true,
	"style":    true,
	"refactor": true,
	"perf":     true,
	"test":     true,
	"build":    true,
	"ci":       true,
	"chore":    true,
	"hotfix":   true,
}

var allowedCommitTypes = map[string]bool{
	"feat":     true,
	"fix":      true,
	"docs":     true,
	"style":    true,
	"refactor": true,
	"perf":     true,
	"test":     true,
	"build":    true,
	"ci":       true,
	"chore":    true,
	"revert":   true,
}

// headerRegex captures Conventional Commits 1.0.0 header:
// <type>[(<scope>)]?[!]?: <description>
var headerRegex = regexp.MustCompile(`^([a-z]+)(?:\(([^)]+)\))?(!)?:\s+(.*)$`)

func validateBranch(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("branch name cannot be empty")
	}

	// Exemptions: main, master, release-please--*, dependabot/*
	if branch == "main" || branch == "master" {
		return nil
	}
	if strings.HasPrefix(branch, "release-please--") {
		return nil
	}
	if strings.HasPrefix(branch, "dependabot/") {
		return nil
	}

	parts := strings.Split(branch, "/")
	if len(parts) != 2 {
		return fmt.Errorf("branch %q must follow format <type>/<short-kebab-case-description>", branch)
	}

	branchType := parts[0]
	desc := parts[1]

	if !allowedBranchTypes[branchType] {
		return fmt.Errorf("invalid branch type %q, allowed: feat, fix, docs, style, refactor, perf, test, build, ci, chore, hotfix", branchType)
	}

	if desc == "" {
		return errors.New("branch description cannot be empty")
	}

	// Description must be lowercase alphanumeric with single hyphens
	// Reject consecutive hyphens, leading/trailing hyphens, uppercase, underscores
	if strings.HasPrefix(desc, "-") || strings.HasSuffix(desc, "-") {
		return fmt.Errorf("branch description %q cannot start or end with a hyphen", desc)
	}
	if strings.Contains(desc, "--") {
		return fmt.Errorf("branch description %q cannot contain consecutive hyphens", desc)
	}

	for _, r := range desc {
		if !unicode.IsLower(r) && !unicode.IsDigit(r) && r != '-' {
			return fmt.Errorf("branch description %q contains invalid character %q (only lowercase alphanumeric and hyphens allowed)", desc, r)
		}
	}

	return nil
}

func validateHeader(header string) error {
	header = strings.TrimRight(header, "\r\n")
	if header == "" {
		return errors.New("commit header cannot be empty")
	}

	// Exemptions for merge commits and Release Please automated commits
	if strings.HasPrefix(header, "Merge branch ") ||
		strings.HasPrefix(header, "Merge pull request ") ||
		strings.HasPrefix(header, "Merge made by the ") ||
		strings.HasPrefix(header, "chore(main): release ") {
		return nil
	}

	matches := headerRegex.FindStringSubmatch(header)
	if matches == nil {
		return fmt.Errorf("header %q does not match Conventional Commits format <type>[(scope)][!]: <description>", header)
	}

	commitType := matches[1]
	scope := matches[2]
	description := matches[4]

	if !allowedCommitTypes[commitType] {
		return fmt.Errorf("invalid commit type %q, allowed: feat, fix, docs, style, refactor, perf, test, build, ci, chore, revert", commitType)
	}

	if strings.Contains(header, "()") {
		return errors.New("scope cannot be empty if parentheses are present")
	}
	_ = scope

	description = strings.TrimSpace(description)
	if description == "" {
		return errors.New("commit description cannot be empty")
	}

	if strings.HasSuffix(description, ".") {
		return errors.New("commit header description must not end with a period")
	}

	firstRune := []rune(description)[0]
	if unicode.IsUpper(firstRune) {
		return fmt.Errorf("commit header description must start with a lowercase character, got %q", string(firstRune))
	}

	return nil
}

func validatePRTitle(title string) error {
	return validateHeader(title)
}

func validateCommit(msg string) error {
	trimmed := strings.TrimSpace(msg)
	if trimmed == "" {
		return errors.New("commit message cannot be empty")
	}

	// Strip trailing carriage returns and split by newline
	normalized := strings.ReplaceAll(msg, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	header := lines[0]

	if err := validateHeader(header); err != nil {
		return err
	}

	// If there are more lines, the second line (index 1) must be blank
	if len(lines) > 1 {
		secondLine := strings.TrimSpace(lines[1])
		if secondLine != "" {
			return errors.New("commit message must separate header from body with an empty line")
		}
	}

	return nil
}

// CommitInfo holds git commit metadata for range validation.
type CommitInfo struct {
	SHA     string
	Message string
}

// validateCommitList validates each commit message in the provided slice.
func validateCommitList(commits []CommitInfo) []error {
	var errs []error
	for _, c := range commits {
		if err := validateCommit(c.Message); err != nil {
			shortSHA := c.SHA
			if len(shortSHA) > 7 {
				shortSHA = shortSHA[:7]
			}
			if shortSHA != "" {
				errs = append(errs, fmt.Errorf("commit %s: %w", shortSHA, err))
			} else {
				errs = append(errs, err)
			}
		}
	}
	return errs
}

func getCommitsInRange(base, head string) ([]CommitInfo, error) {
	commitRange := fmt.Sprintf("%s..%s", base, head)
	cmd := exec.Command("git", "log", "--format=%H%x1f%B%x1e", commitRange)
	out, err := cmd.Output()
	if err != nil {
		// Fallback: resolve merge-base if direct range fails (e.g. diverged branches)
		mbCmd := exec.Command("git", "merge-base", base, head)
		mbOut, mbErr := mbCmd.Output()
		if mbErr == nil {
			mb := strings.TrimSpace(string(mbOut))
			if mb != "" {
				cmd = exec.Command("git", "log", "--format=%H%x1f%B%x1e", fmt.Sprintf("%s..%s", mb, head))
				out, err = cmd.Output()
			}
		}
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve git commits for range %q: %w", commitRange, err)
		}
	}

	records := strings.Split(string(out), "\x1e")
	var commits []CommitInfo
	for _, rec := range records {
		rec = strings.TrimSpace(rec)
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\x1f", 2)
		if len(parts) < 2 {
			continue
		}
		sha := strings.TrimSpace(parts[0])
		msg := strings.TrimSpace(parts[1])
		commits = append(commits, CommitInfo{
			SHA:     sha,
			Message: msg,
		})
	}
	return commits, nil
}

func runPRCommitsCmd(args []string) error {
	fs := flag.NewFlagSet("pr-commits", flag.ContinueOnError)
	baseFlag := fs.String("base", "", "Base git commit or ref")
	headFlag := fs.String("head", "HEAD", "Head git commit or ref")
	if err := fs.Parse(args); err != nil {
		return err
	}

	base := *baseFlag
	head := *headFlag

	if base == "" && fs.NArg() >= 1 {
		base = fs.Arg(0)
		if fs.NArg() >= 2 {
			head = fs.Arg(1)
		}
	}

	if base == "" {
		if exec.Command("git", "rev-parse", "--verify", "origin/main").Run() == nil {
			base = "origin/main"
		} else if exec.Command("git", "rev-parse", "--verify", "main").Run() == nil {
			base = "main"
		} else {
			return errors.New("pr-commits requires --base flag (or base positional argument)")
		}
	}

	commits, err := getCommitsInRange(base, head)
	if err != nil {
		return err
	}

	if len(commits) == 0 {
		fmt.Printf("✓ No commits found in range %s..%s to validate\n", base, head)
		return nil
	}

	errs := validateCommitList(commits)
	if len(errs) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%d commit(s) failed Conventional Commits validation in range %s..%s:\n", len(errs), base, head)
		for _, e := range errs {
			fmt.Fprintf(&b, "  - %v\n", e)
		}
		return errors.New(b.String())
	}

	fmt.Printf("✓ All %d commit(s) in range %s..%s are valid Conventional Commits\n", len(commits), base, head)
	return nil
}

func getCurrentGitBranch() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get git branch: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func runBranchCmd(args []string) error {
	var branch string
	if len(args) > 0 {
		branch = args[0]
	} else {
		var err error
		branch, err = getCurrentGitBranch()
		if err != nil {
			return err
		}
	}

	if err := validateBranch(branch); err != nil {
		return fmt.Errorf("branch validation failed: %w", err)
	}
	fmt.Printf("✓ Branch %q is valid\n", branch)
	return nil
}

func runCommitCmd(args []string) error {
	fs := flag.NewFlagSet("commit", flag.ContinueOnError)
	msgFlag := fs.String("msg", "", "Commit message string to validate")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var commitMsg string
	if *msgFlag != "" {
		commitMsg = *msgFlag
	} else if fs.NArg() > 0 {
		filePath := fs.Arg(0)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read commit message file %q: %w", filePath, err)
		}
		commitMsg = string(data)
	} else {
		return errors.New("commit command requires a file path or --msg flag")
	}

	if err := validateCommit(commitMsg); err != nil {
		return fmt.Errorf("commit message validation failed: %w", err)
	}
	fmt.Println("✓ Commit message is valid")
	return nil
}

func runPRTitleCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("pr-title command requires a title argument")
	}
	title := strings.Join(args, " ")
	if err := validatePRTitle(title); err != nil {
		return fmt.Errorf("PR title validation failed: %w", err)
	}
	fmt.Printf("✓ PR title %q is valid\n", title)
	return nil
}

type releasePleasePkgConfig struct {
	ReleaseType           string `json:"release-type"`
	PackageName           string `json:"package-name"`
	InitialVersion        string `json:"initial-version"`
	BumpMinorPreMajor     *bool  `json:"bump-minor-pre-major"`
	IncludeComponentInTag *bool  `json:"include-component-in-tag"`
}

type releasePleaseRootConfig struct {
	ReleaseType string                            `json:"release-type"`
	Packages    map[string]releasePleasePkgConfig `json:"packages"`
}

func validateReleaseConfig(dir ...string) error {
	baseDir := "."
	if len(dir) > 0 && dir[0] != "" {
		baseDir = dir[0]
	} else if _, err := os.Stat("release-please-config.json"); err != nil {
		// Walk upwards to find repository root containing go.mod (e.g. during `go test` in subdirectories)
		if cwd, err := os.Getwd(); err == nil {
			cur := cwd
			for {
				if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
					baseDir = cur
					break
				}
				parent := filepath.Dir(cur)
				if parent == cur {
					break
				}
				cur = parent
			}
		}
	}

	// 1. Validate release-please-config.json
	configPath := filepath.Join(baseDir, "release-please-config.json")
	configData, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read release-please config %q: %w", configPath, err)
	}

	var rootCfg releasePleaseRootConfig
	if err := json.Unmarshal(configData, &rootCfg); err != nil {
		return fmt.Errorf("failed to parse %q as JSON: %w", configPath, err)
	}

	if rootCfg.Packages == nil {
		return fmt.Errorf("%q missing required 'packages' configuration", configPath)
	}

	rootPkg, ok := rootCfg.Packages["."]
	if !ok {
		return fmt.Errorf("%q missing configuration for package '.'", configPath)
	}

	// Check package-name: "election-bot"
	if rootPkg.PackageName != "election-bot" {
		return fmt.Errorf("%q package '.' must configure package-name %q, got %q", configPath, "election-bot", rootPkg.PackageName)
	}

	// Check release-type: "go" (either on package or inherited from root)
	relType := rootPkg.ReleaseType
	if relType == "" {
		relType = rootCfg.ReleaseType
	}
	if relType != "go" {
		return fmt.Errorf("%q package '.' must configure release-type %q, got %q", configPath, "go", relType)
	}

	// Check initial-version: "0.1.0"
	if rootPkg.InitialVersion != "0.1.0" {
		return fmt.Errorf("%q package '.' must configure initial-version %q, got %q", configPath, "0.1.0", rootPkg.InitialVersion)
	}

	// Check bump-minor-pre-major: true
	if rootPkg.BumpMinorPreMajor == nil || !*rootPkg.BumpMinorPreMajor {
		return fmt.Errorf("%q package '.' must configure bump-minor-pre-major: true", configPath)
	}

	// Check include-component-in-tag: false
	if rootPkg.IncludeComponentInTag == nil || *rootPkg.IncludeComponentInTag {
		return fmt.Errorf("%q package '.' must configure include-component-in-tag: false", configPath)
	}

	// 2. Validate .release-please-manifest.json
	manifestPath := filepath.Join(baseDir, ".release-please-manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read release-please manifest %q: %w", manifestPath, err)
	}

	var manifest map[string]string
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("failed to parse %q as JSON: %w", manifestPath, err)
	}

	ver, ok := manifest["."]
	if !ok {
		return fmt.Errorf("%q missing version entry for package '.'", manifestPath)
	}
	if ver != "0.1.0" {
		return fmt.Errorf("%q package '.' version must be %q, got %q", manifestPath, "0.1.0", ver)
	}

	// 3. Validate CHANGELOG.md
	changelogPath := filepath.Join(baseDir, "CHANGELOG.md")
	changelogData, err := os.ReadFile(changelogPath)
	if err != nil {
		return fmt.Errorf("failed to read changelog %q: %w", changelogPath, err)
	}

	changelogStr := strings.TrimSpace(string(changelogData))
	if changelogStr == "" {
		return fmt.Errorf("%q is empty", changelogPath)
	}

	// Verify it contains a markdown header (# ...)
	hasHeader := false
	for _, line := range strings.Split(changelogStr, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "#") {
			hasHeader = true
			break
		}
	}
	if !hasHeader {
		return fmt.Errorf("%q must contain an initial markdown header (e.g. # Changelog)", changelogPath)
	}

	return nil
}

func runReleaseConfigCmd(args []string) error {
	baseDir := "."
	if len(args) > 0 {
		baseDir = args[0]
	}
	if err := validateReleaseConfig(baseDir); err != nil {
		return fmt.Errorf("release config validation failed: %w", err)
	}
	fmt.Println("✓ Release configuration is valid")
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: dev-check <branch|commit|pr-commits|pr-title|release-config> [options]\n")
		os.Exit(1)
	}

	subcommand := os.Args[1]
	subArgs := os.Args[2:]

	var err error
	switch subcommand {
	case "branch":
		err = runBranchCmd(subArgs)
	case "commit":
		err = runCommitCmd(subArgs)
	case "pr-commits":
		err = runPRCommitsCmd(subArgs)
	case "pr-title":
		err = runPRTitleCmd(subArgs)
	case "release-config":
		err = runReleaseConfigCmd(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand %q. Available: branch, commit, pr-commits, pr-title, release-config\n", subcommand)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
