package reconciliation

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestCICompatibilityPolicy(t *testing.T) {
	contents, err := os.ReadFile(".github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read CI workflow: %v", err)
	}
	workflow := string(contents)

	permissions := yamlIndentedBlock(workflow, "permissions:")
	if strings.TrimSpace(permissions) != "contents: read" {
		t.Errorf("permissions must contain only contents: read, got:\n%s", permissions)
	}

	jobs := yamlIndentedBlock(workflow, "jobs:")
	matrixJob := yamlBlockContainingAll(jobs, 2, []string{
		"'1.22.x'", "'1.23.x'", "'1.24.x'", "'1.25.x'", "'1.26.x'",
	})
	if matrixJob == "" {
		t.Fatal("no job contains the complete supported Go version matrix")
	}

	versionList := yamlIndentedBlock(matrixJob, "go-version:")
	versionPattern := regexp.MustCompile(`(?m)^\s*-\s*'([^']+)'\s*$`)
	matches := versionPattern.FindAllStringSubmatch(versionList, -1)
	wantVersions := []string{"1.22.x", "1.23.x", "1.24.x", "1.25.x", "1.26.x"}
	if len(matches) != len(wantVersions) {
		t.Fatalf("Go matrix has %d versions, want %d", len(matches), len(wantVersions))
	}
	for i, want := range wantVersions {
		if matches[i][1] != want {
			t.Errorf("Go matrix version %d = %q, want %q", i, matches[i][1], want)
		}
	}

	requireWorkflowText(t, matrixJob,
		"runs-on: ubuntu-latest",
		"fail-fast: false",
		"uses: actions/checkout@v6",
		"uses: actions/setup-go@v6",
		"cache: false",
		"GOPRIVATE: github.com/justusschock/*",
		"GONOSUMDB: github.com/justusschock/*",
		"go mod verify",
		"go mod tidy -diff",
		"go vet ./...",
		"go test -race -count=1 ./...",
	)
	if !regexp.MustCompile(`(?m)^\s+timeout-minutes:\s*[1-9][0-9]*\s*$`).MatchString(matrixJob) {
		t.Error("matrix job must have a finite positive timeout-minutes value")
	}

	secretReference := "${{ secrets.GO_MODULES_TOKEN }}"
	if strings.Count(workflow, secretReference) != 1 {
		t.Errorf("workflow must reference the GO_MODULES_TOKEN secret exactly once, got %d", strings.Count(workflow, secretReference))
	}
	credentialStep := yamlStepContaining(matrixJob, "GO_MODULES_TOKEN: "+secretReference)
	if credentialStep == "" {
		t.Fatal("GO_MODULES_TOKEN must be supplied through a step environment")
	}
	requireWorkflowText(t, credentialStep,
		"env:",
		"GO_MODULES_TOKEN: "+secretReference,
		`if [ -z "$GO_MODULES_TOKEN" ]; then`,
		`echo "GO_MODULES_TOKEN is required" >&2`,
		`credentials_file="$RUNNER_TEMP/git-credentials"`,
		"umask 077",
		`"$GO_MODULES_TOKEN" > "$credentials_file"`,
		`chmod 0600 "$credentials_file"`,
		`git config --global credential.helper "store --file=$credentials_file"`,
	)
	if regexp.MustCompile(`https?://[^\n]*(?:secrets\.GO_MODULES_TOKEN|\$\{\{\s*secrets\.GO_MODULES_TOKEN\s*\}\})`).MatchString(workflow) {
		t.Error("GO_MODULES_TOKEN secret must not be interpolated into a URL")
	}

	cleanupStep := yamlStepContaining(matrixJob, "if: always()")
	if cleanupStep == "" {
		t.Fatal("credential cleanup must run with if: always()")
	}
	requireWorkflowText(t, cleanupStep,
		"if: always()",
		`rm -f "$RUNNER_TEMP/git-credentials"`,
	)

	triggers := yamlIndentedBlock(workflow, "on:")
	requireWorkflowText(t, triggers, "push:", "pull_request:")
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

func yamlStepContaining(contents, requirement string) string {
	lines := strings.Split(contents, "\n")
	for index, line := range lines {
		if !strings.Contains(line, requirement) {
			continue
		}
		start := index
		for start >= 0 && !strings.HasPrefix(strings.TrimSpace(lines[start]), "- ") {
			start--
		}
		if start < 0 {
			return ""
		}
		indent := leadingSpaces(lines[start])
		end := start + 1
		for end < len(lines) {
			if leadingSpaces(lines[end]) == indent && strings.HasPrefix(strings.TrimSpace(lines[end]), "- ") {
				break
			}
			end++
		}
		return strings.Join(lines[start:end], "\n")
	}
	return ""
}

func leadingSpaces(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}
