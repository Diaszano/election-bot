package docker

import (
	"bufio"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type parsedJob struct {
	name           string
	timeoutMinutes int
	steps          []parsedStep
	lines          []string
}

type parsedStep struct {
	name string
	uses string
	run  string
	with map[string]string
}

func parseWorkflow(content string) (topLevels map[string][]string, jobs map[string]*parsedJob, err error) {
	topLevels = make(map[string][]string)
	jobs = make(map[string]*parsedJob)

	scanner := bufio.NewScanner(strings.NewReader(content))
	var currentTopLevel string
	var currentJobName string
	var currentJob *parsedJob
	var inJobs bool

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		rawLine := scanner.Text()
		trimmed := strings.TrimSpace(rawLine)

		// Check for tabs in indentation
		leadingSpaces := len(rawLine) - len(strings.TrimLeft(rawLine, " "))
		leadingTabs := len(rawLine) - len(strings.TrimLeft(rawLine, "\t"))
		if leadingTabs > 0 && leadingSpaces == 0 {
			return nil, nil, fmt.Errorf("line %d: YAML must not use tabs for indentation", lineNum)
		}

		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Top-level key (0 indentation)
		if !strings.HasPrefix(rawLine, " ") && strings.Contains(trimmed, ":") {
			parts := strings.SplitN(trimmed, ":", 2)
			currentTopLevel = strings.TrimSpace(parts[0])
			currentTopLevel = strings.Trim(currentTopLevel, `"'`)
			topLevels[currentTopLevel] = append(topLevels[currentTopLevel], strings.TrimSpace(parts[1]))
			if currentTopLevel == "jobs" {
				inJobs = true
			} else {
				inJobs = false
				currentJob = nil
			}
			continue
		}

		if !inJobs {
			topLevels[currentTopLevel] = append(topLevels[currentTopLevel], trimmed)
			continue
		}

		// Inside jobs: indentation of 2 spaces defines a job name
		if strings.HasPrefix(rawLine, "  ") && !strings.HasPrefix(rawLine, "   ") && strings.HasSuffix(trimmed, ":") {
			currentJobName = strings.TrimSuffix(trimmed, ":")
			currentJob = &parsedJob{name: currentJobName}
			jobs[currentJobName] = currentJob
			continue
		}

		if currentJob != nil {
			currentJob.lines = append(currentJob.lines, trimmed)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}

	// Parse job properties and steps
	timeoutRegex := regexp.MustCompile(`^timeout-minutes:\s*(\d+)`)
	for _, job := range jobs {
		var curStep *parsedStep
		var inWith bool

		for _, line := range job.lines {
			if m := timeoutRegex.FindStringSubmatch(line); len(m) > 1 {
				to, _ := strconv.Atoi(m[1])
				job.timeoutMinutes = to
			}

			if strings.HasPrefix(line, "- name:") {
				curStep = &parsedStep{
					name: strings.TrimSpace(strings.TrimPrefix(line, "- name:")),
					with: make(map[string]string),
				}
				job.steps = append(job.steps, *curStep)
				curStep = &job.steps[len(job.steps)-1]
				inWith = false
				continue
			}

			if strings.HasPrefix(line, "- uses:") {
				action := strings.TrimSpace(strings.TrimPrefix(line, "- uses:"))
				curStep = &parsedStep{
					uses: action,
					with: make(map[string]string),
				}
				job.steps = append(job.steps, *curStep)
				curStep = &job.steps[len(job.steps)-1]
				inWith = false
				continue
			}

			if curStep != nil {
				if strings.HasPrefix(line, "uses:") {
					curStep.uses = strings.TrimSpace(strings.TrimPrefix(line, "uses:"))
					inWith = false
				} else if strings.HasPrefix(line, "run:") {
					curStep.run = strings.TrimSpace(strings.TrimPrefix(line, "run:"))
					inWith = false
				} else if strings.HasPrefix(line, "with:") {
					inWith = true
				} else if inWith && strings.Contains(line, ":") {
					parts := strings.SplitN(line, ":", 2)
					k := strings.TrimSpace(parts[0])
					v := strings.TrimSpace(parts[1])
					v = strings.Trim(v, `"'`)
					curStep.with[k] = v
				} else if !strings.Contains(line, ":") {
					// Multiline run continuation
					if curStep.run != "" {
						curStep.run += "\n" + line
					}
				}
			}
		}
	}

	return topLevels, jobs, nil
}

func TestCIWorkflow(t *testing.T) {
	content := readRootFile(t, ".github/workflows/ci.yml")

	topLevels, jobs, err := parseWorkflow(content)
	if err != nil {
		t.Fatalf("invalid YAML syntax in .github/workflows/ci.yml: %v", err)
	}

	// 1. Top-level triggers: pull_request (targeting main), push (to main), workflow_dispatch
	onLines, ok := topLevels["on"]
	if !ok {
		t.Fatalf(".github/workflows/ci.yml missing 'on' trigger section")
	}
	onContent := strings.Join(onLines, "\n")

	if !strings.Contains(onContent, "push:") || !hasBranch(onLines, "push", "main") {
		t.Errorf("expected 'push' trigger targeting 'main'")
	}
	if !strings.Contains(onContent, "pull_request:") || !hasBranch(onLines, "pull_request", "main") {
		t.Errorf("expected 'pull_request' trigger targeting 'main'")
	}
	if !strings.Contains(onContent, "workflow_dispatch") {
		t.Errorf("expected 'workflow_dispatch' trigger")
	}

	// 2. Asserts permissions: contents: read is set
	permLines, ok := topLevels["permissions"]
	if !ok {
		t.Fatalf(".github/workflows/ci.yml missing top-level 'permissions' section")
	}
	permContent := strings.Join(permLines, " ")
	if !strings.Contains(permContent, "contents: read") {
		t.Errorf("expected top-level permissions to contain 'contents: read', got %q", permContent)
	}

	// 3. Asserts concurrency group is configured with cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}
	concurLines, ok := topLevels["concurrency"]
	if !ok {
		t.Fatalf(".github/workflows/ci.yml missing top-level 'concurrency' section")
	}
	concurContent := strings.Join(concurLines, "\n")
	if !strings.Contains(concurContent, "group:") {
		t.Errorf("expected concurrency group to be configured")
	}
	expectedCancel := "cancel-in-progress: ${{ github.ref != 'refs/heads/main' }}"
	if !strings.Contains(concurContent, expectedCancel) {
		t.Errorf("expected concurrency to specify %q, got: %s", expectedCancel, concurContent)
	}

	// 4. Asserts required jobs are present: standards, go-quality, security, docker-validation
	requiredJobs := []string{"standards", "go-quality", "security", "docker-validation"}
	for _, req := range requiredJobs {
		job, ok := jobs[req]
		if !ok {
			t.Errorf("missing required job: %q", req)
			continue
		}

		// 5. Asserts all jobs have explicit timeout-minutes
		if job.timeoutMinutes <= 0 {
			t.Errorf("job %q missing explicit timeout-minutes (> 0)", req)
		}
	}

	// 6. Asserts all actions are pinned to 40-character commit SHAs
	actionShaRegex := regexp.MustCompile(`^[a-zA-Z0-9_\-\./]+@[0-9a-f]{40}$`)
	for jobName, job := range jobs {
		for _, step := range job.steps {
			if step.uses == "" {
				continue
			}
			// Strip comment if any (e.g. # v4.2.2)
			actionRef := strings.TrimSpace(strings.Split(step.uses, "#")[0])
			if !actionShaRegex.MatchString(actionRef) {
				t.Errorf("job %q step %q action %q is not pinned to a 40-character commit SHA",
					jobName, step.name, actionRef)
			}
		}
	}

	// 7. Verify Job 'standards'
	if job, ok := jobs["standards"]; ok {
		jobStr := strings.Join(job.lines, "\n")
		if !strings.Contains(jobStr, "fetch-depth: 0") {
			t.Errorf("standards job must check out code with fetch-depth: 0")
		}
		if !strings.Contains(jobStr, "go run ./cmd/dev-check branch") {
			t.Errorf("standards job must run 'go run ./cmd/dev-check branch'")
		}
		if !strings.Contains(jobStr, `go run ./cmd/dev-check pr-title "${{ github.event.pull_request.title }}"`) {
			t.Errorf("standards job must run 'go run ./cmd/dev-check pr-title \"${{ github.event.pull_request.title }}\"' on PRs")
		}
		if !strings.Contains(jobStr, `go run ./cmd/dev-check pr-commits --base "${{ github.event.pull_request.base.sha }}" --head "${{ github.event.pull_request.head.sha }}"`) {
			t.Errorf("standards job must run 'go run ./cmd/dev-check pr-commits' validating all commits in PR range")
		}
		if !strings.Contains(jobStr, "go run ./cmd/dev-check release-config") {
			t.Errorf("standards job must run 'go run ./cmd/dev-check release-config'")
		}
	}

	// 8. Verify Job 'go-quality'
	if job, ok := jobs["go-quality"]; ok {
		jobStr := strings.Join(job.lines, "\n")
		if !strings.Contains(jobStr, "1.27.0") && !strings.Contains(jobStr, "'1.27.0'") {
			t.Errorf("go-quality job must use Go 1.27.0")
		}
		if !strings.Contains(jobStr, "go mod verify") {
			t.Errorf("go-quality job must run 'go mod verify'")
		}
		if !strings.Contains(jobStr, "go vet ./...") {
			t.Errorf("go-quality job must run 'go vet ./...'")
		}
		if !strings.Contains(jobStr, "make lint") && !strings.Contains(jobStr, "golangci-lint run") {
			t.Errorf("go-quality job must run 'make lint' or 'golangci-lint run'")
		}
		if !strings.Contains(jobStr, "go test -count=1 ./...") {
			t.Errorf("go-quality job must run 'go test -count=1 ./...'")
		}
		if !strings.Contains(jobStr, "go test -race ./...") {
			t.Errorf("go-quality job must run 'go test -race ./...'")
		}
		if !strings.Contains(jobStr, "make simulate") {
			t.Errorf("go-quality job must run 'make simulate'")
		}
		if !strings.Contains(jobStr, "CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/election-bot") {
			t.Errorf("go-quality job must run 'CGO_ENABLED=0 go build -trimpath -o /dev/null ./cmd/election-bot'")
		}
	}

	// 9. Verify Job 'security'
	if job, ok := jobs["security"]; ok {
		jobStr := strings.Join(job.lines, "\n")
		if !strings.Contains(jobStr, "golang/govulncheck-action@b331f47137f8f83734661845cc877fa6992d9aa3") {
			t.Errorf("security job must run govulncheck via golang/govulncheck-action@b331f47137f8f83734661845cc877fa6992d9aa3")
		}
		if !strings.Contains(jobStr, "gitleaks/gitleaks-action@f9076d2be247bf23703975ba6fe6e0362bc462b1") {
			t.Errorf("security job must run gitleaks via gitleaks/gitleaks-action@f9076d2be247bf23703975ba6fe6e0362bc462b1")
		}
		if !strings.Contains(jobStr, "actions/dependency-review-action@5a2ce3f5b92ee19cbb1541a4984c76d921601d7c") {
			t.Errorf("security job must run dependency-review via actions/dependency-review-action@5a2ce3f5b92ee19cbb1541a4984c76d921601d7c")
		}
	}

	// 10. Verify Job 'docker-validation'
	if job, ok := jobs["docker-validation"]; ok {
		jobStr := strings.Join(job.lines, "\n")
		if !strings.Contains(jobStr, "docker/setup-buildx-action") {
			t.Errorf("docker-validation job must use Docker Buildx")
		}
		if !strings.Contains(jobStr, "aquasecurity/trivy-action@18f2510ee396bbf400402947b394f2dd8c87dbb0") {
			t.Errorf("docker-validation job must run Trivy via aquasecurity/trivy-action@18f2510ee396bbf400402947b394f2dd8c87dbb0")
		}
		if !strings.Contains(jobStr, "CRITICAL") {
			t.Errorf("docker-validation job must configure Trivy failing on CRITICAL")
		}
		if !strings.Contains(jobStr, "65532") || !strings.Contains(jobStr, "/health/ready") || !strings.Contains(jobStr, "version") {
			t.Errorf("docker-validation job must run smoke tests verifying non-root UID 65532, /health/ready, and version CLI")
		}
	}
}

func hasBranch(lines []string, trigger, branch string) bool {
	inTrigger := false
	for _, l := range lines {
		if strings.HasPrefix(l, trigger+":") {
			inTrigger = true
			continue
		}
		if inTrigger {
			if strings.HasSuffix(l, ":") && !strings.Contains(l, "branches:") {
				break
			}
			if strings.Contains(l, branch) {
				return true
			}
		}
	}
	return false
}
