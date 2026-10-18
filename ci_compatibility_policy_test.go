package reconciliation

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// NFR-RECON-05: CI is least-privilege and reproducible: only push and
// pull_request triggers, read-only permissions, actions pinned to commit
// SHAs, no secrets or private-module configuration, every job bounded by a
// timeout, and the test matrix covering the Go floor and both supported
// upstream releases.
func TestCIWorkflowPolicy(t *testing.T) {
	contents, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	workflow := string(contents)

	triggers := yamlIndentedBlock(workflow, "on:")
	requireWorkflowText(t, triggers, "push:", "pull_request:")
	for _, forbidden := range []string{"pull_request_target", "workflow_run"} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("workflow must not use the %s trigger", forbidden)
		}
	}

	permissions := yamlIndentedBlock(workflow, "permissions:")
	if strings.TrimSpace(permissions) != "contents: read" {
		t.Errorf("permissions must contain only contents: read, got:\n%s", permissions)
	}

	for _, forbidden := range []string{"GOPRIVATE", "GONOSUMDB", "GO_MODULES_TOKEN", "secrets.", ".netrc", "insteadOf"} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("workflow must not reference %q", forbidden)
		}
	}

	usesLine := regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s*(\S+)(.*)$`)
	pinned := regexp.MustCompile(`^[\w.-]+/[\w./-]+@[0-9a-f]{40}$`)
	uses := usesLine.FindAllStringSubmatch(workflow, -1)
	if len(uses) == 0 {
		t.Fatal("workflow uses no actions")
	}
	for _, match := range uses {
		if !pinned.MatchString(match[1]) || !strings.Contains(match[2], "# v") {
			t.Errorf("action %q must be pinned to a full commit SHA with a version comment", match[1])
		}
	}
	checkouts := strings.Count(workflow, "uses: actions/checkout@")
	if got := strings.Count(workflow, "persist-credentials: false"); got != checkouts {
		t.Errorf("%d checkouts but %d set persist-credentials: false", checkouts, got)
	}

	jobs := yamlIndentedBlock(workflow, "jobs:")
	jobHeader := regexp.MustCompile(`(?m)^  [\w-]+:\s*$`)
	jobTimeout := regexp.MustCompile(`(?m)^    timeout-minutes:\s*[1-9][0-9]*\s*$`)
	jobCount := len(jobHeader.FindAllString(jobs, -1))
	if jobCount == 0 {
		t.Fatal("workflow defines no jobs")
	}
	if got := len(jobTimeout.FindAllString(jobs, -1)); got != jobCount {
		t.Errorf("%d jobs but %d job-level timeout-minutes values", jobCount, got)
	}

	testJob := yamlBlockContainingAll(jobs, 2, []string{"go-version:", "go test"})
	if testJob == "" {
		t.Fatal("no test job with a Go version matrix")
	}
	requireWorkflowText(t, testJob,
		"'1.25.x'", "'oldstable'", "'stable'",
		"go mod verify",
		"go vet ./...",
		"go test -race -count=1 ./...",
	)
	if yamlBlockContainingAll(jobs, 2, []string{"golangci/golangci-lint-action@"}) == "" {
		t.Error("no lint job runs golangci-lint")
	}
}

func requireWorkflowText(t *testing.T, contents string, requirements ...string) {
	t.Helper()
	for _, requirement := range requirements {
		if !strings.Contains(contents, requirement) {
			t.Errorf("CI workflow is missing %q", requirement)
		}
	}
}

func yamlIndentedBlock(contents, key string) string {
	lines := strings.Split(contents, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) != key {
			continue
		}
		indent := leadingSpaces(line)
		end := index + 1
		for end < len(lines) {
			if strings.TrimSpace(lines[end]) != "" && leadingSpaces(lines[end]) <= indent {
				break
			}
			end++
		}
		return strings.Join(lines[index+1:end], "\n")
	}
	return ""
}

func yamlBlockContainingAll(contents string, indent int, requirements []string) string {
	lines := strings.Split(contents, "\n")
	for index, line := range lines {
		if leadingSpaces(line) != indent || !strings.HasSuffix(strings.TrimSpace(line), ":") {
			continue
		}
		end := index + 1
		for end < len(lines) {
			if strings.TrimSpace(lines[end]) != "" && leadingSpaces(lines[end]) <= indent {
				break
			}
			end++
		}
		block := strings.Join(lines[index:end], "\n")
		containsAll := true
		for _, requirement := range requirements {
			containsAll = containsAll && strings.Contains(block, requirement)
		}
		if containsAll {
			return block
		}
	}
	return ""
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}
