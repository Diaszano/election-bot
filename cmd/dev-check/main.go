package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: dev-check <branch|commit|pr-title> [options]\n")
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
	case "pr-title":
		err = runPRTitleCmd(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown subcommand %q. Available: branch, commit, pr-title\n", subcommand)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
