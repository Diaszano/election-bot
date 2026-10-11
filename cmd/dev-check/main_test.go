package main

import (
	"testing"
)

func TestValidateBranch(t *testing.T) {
	tests := []struct {
		name    string
		branch  string
		wantErr bool
	}{
		// Valid branches
		{name: "feature branch", branch: "feat/add-login", wantErr: false},
		{name: "fix branch", branch: "fix/issue-123", wantErr: false},
		{name: "docs branch", branch: "docs/readme-update", wantErr: false},
		{name: "style branch", branch: "style/format-code", wantErr: false},
		{name: "refactor branch", branch: "refactor/auth-pipeline", wantErr: false},
		{name: "perf branch", branch: "perf/db-query", wantErr: false},
		{name: "test branch", branch: "test/unit-tests", wantErr: false},
		{name: "build branch", branch: "build/docker-optimize", wantErr: false},
		{name: "ci branch", branch: "ci/github-actions-release-pipeline", wantErr: false},
		{name: "chore branch", branch: "chore/update-deps", wantErr: false},
		{name: "hotfix branch", branch: "hotfix/urgent-patch", wantErr: false},
		{name: "with numbers", branch: "feat/oauth2-support", wantErr: false},
		{name: "single word description", branch: "feat/login", wantErr: false},

		// Exemptions
		{name: "exemption main", branch: "main", wantErr: false},
		{name: "exemption master", branch: "master", wantErr: false},
		{name: "exemption release-please", branch: "release-please--branches--main", wantErr: false},
		{name: "exemption dependabot", branch: "dependabot/go_modules/foo-1.0.0", wantErr: false},

		// Invalid branches
		{name: "empty branch", branch: "", wantErr: true},
		{name: "unknown type", branch: "unknown/something", wantErr: true},
		{name: "missing slash", branch: "feat-add-login", wantErr: true},
		{name: "empty description", branch: "feat/", wantErr: true},
		{name: "consecutive hyphens", branch: "feat/foo--bar", wantErr: true},
		{name: "leading hyphen", branch: "feat/-foo", wantErr: true},
		{name: "trailing hyphen", branch: "feat/foo-", wantErr: true},
		{name: "uppercase in type", branch: "FEAT/foo", wantErr: true},
		{name: "uppercase in description", branch: "feat/FooBar", wantErr: true},
		{name: "underscore in description", branch: "feat/foo_bar", wantErr: true},
		{name: "multiple slashes", branch: "feat/nested/branch", wantErr: true},
		{name: "spaces in description", branch: "feat/foo bar", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBranch(tt.branch)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBranch(%q) error = %v, wantErr %v", tt.branch, err, tt.wantErr)
			}
		})
	}
}

func TestValidatePRTitle(t *testing.T) {
	tests := []struct {
		name    string
		title   string
		wantErr bool
	}{
		// Valid PR titles
		{name: "simple feat", title: "feat: add user authentication", wantErr: false},
		{name: "feat with scope", title: "feat(auth): add login endpoint", wantErr: false},
		{name: "fix with scope", title: "fix(api): handle timeout correctly", wantErr: false},
		{name: "breaking change exclamation", title: "feat!: drop node 16 support", wantErr: false},
		{name: "breaking change with scope", title: "feat(api)!: remove deprecated v1 endpoints", wantErr: false},
		{name: "chore", title: "chore: update dependencies", wantErr: false},
		{name: "docs", title: "docs: update readme", wantErr: false},
		{name: "revert", title: "revert: rollback bad deploy", wantErr: false},
		{name: "hotfix is not commit type", title: "hotfix: urgent patch", wantErr: true}, // hotfix is for branch, not CC type

		// Invalid PR titles
		{name: "empty title", title: "", wantErr: true},
		{name: "no colon", title: "feat add auth", wantErr: true},
		{name: "uppercase description", title: "feat: Add user authentication", wantErr: true},
		{name: "trailing period", title: "feat: add user authentication.", wantErr: true},
		{name: "missing description", title: "feat:", wantErr: true},
		{name: "space only description", title: "feat:   ", wantErr: true},
		{name: "invalid type", title: "foo: bar", wantErr: true},
		{name: "empty scope", title: "feat(): something", wantErr: true},
		{name: "scope with spaces", title: "feat(my scope): something", wantErr: false}, // scopes can contain spaces/dashes
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePRTitle(tt.title)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePRTitle(%q) error = %v, wantErr %v", tt.title, err, tt.wantErr)
			}
		})
	}
}

func TestValidateCommit(t *testing.T) {
	tests := []struct {
		name    string
		msg     string
		wantErr bool
	}{
		// Valid commits
		{
			name:    "simple one-liner",
			msg:     "feat: add user login",
			wantErr: false,
		},
		{
			name: "header with body",
			msg: `feat: add user login

This adds OAuth2 login support using Google provider.`,
			wantErr: false,
		},
		{
			name: "header with body and footer",
			msg: `fix(auth): correct token validation

Resolves expired token crash issue.

Fixes #123`,
			wantErr: false,
		},
		{
			name: "breaking change in footer",
			msg: `feat(api): overhaul v2 router

BREAKING CHANGE: /v1 endpoints are now disabled.`,
			wantErr: false,
		},
		{
			name: "breaking change with bang and footer",
			msg: `refactor(db)!: switch to postgres connection pool

BREAKING CHANGE: SQLite configuration options removed.`,
			wantErr: false,
		},

		// Exemptions
		{
			name:    "merge branch",
			msg:     "Merge branch 'feat/login' into main",
			wantErr: false,
		},
		{
			name:    "merge pull request",
			msg:     "Merge pull request #42 from org/feat/login",
			wantErr: false,
		},
		{
			name:    "merge strategy",
			msg:     "Merge made by the 'ort' strategy.",
			wantErr: false,
		},
		{
			name:    "release please automated commit",
			msg:     "chore(main): release 1.0.0",
			wantErr: false,
		},

		// Invalid commits
		{
			name:    "empty commit message",
			msg:     "",
			wantErr: true,
		},
		{
			name:    "missing blank line between header and body",
			msg:     "feat: add user login\nDirectly attached body without blank line",
			wantErr: true,
		},
		{
			name:    "trailing period in header",
			msg:     "feat: add user login.",
			wantErr: true,
		},
		{
			name:    "capitalized description",
			msg:     "feat: Add user login",
			wantErr: true,
		},
		{
			name:    "invalid commit type",
			msg:     "hotfix: urgent security patch",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCommit(tt.msg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCommit(%q) error = %v, wantErr %v", tt.msg, err, tt.wantErr)
			}
		})
	}
}
