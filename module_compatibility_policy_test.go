package reconciliation

import (
	"os"
	"strings"
	"testing"
)

func TestModuleCompatibilityPolicy(t *testing.T) {
	contents, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	modulePath, goVersion, requirements, hasToolchain, hasReplace := parseModuleCompatibilityPolicy(string(contents))
	if modulePath != "github.com/justusschock/go-reconciliation" {
		t.Errorf("module path = %q, want %q", modulePath, "github.com/justusschock/go-reconciliation")
	}
	if goVersion != "1.22" {
		t.Errorf("go version = %q, want %q", goVersion, "1.22")
	}
	for _, requirement := range []struct {
		modulePath  string
		wantVersion string
	}{
		{modulePath: "github.com/justusschock/go-concurrency", wantVersion: "v0.2.0"},
		{modulePath: "github.com/justusschock/go-lifecycle", wantVersion: "v0.2.0"},
	} {
		if gotVersion := requirements[requirement.modulePath]; gotVersion != requirement.wantVersion {
			t.Errorf("requirement %s = %q, want %q", requirement.modulePath, gotVersion, requirement.wantVersion)
		}
	}
	if hasToolchain {
		t.Error("go.mod contains a toolchain directive")
	}
	if hasReplace {
		t.Error("go.mod contains a replace directive")
	}
}

func parseModuleCompatibilityPolicy(contents string) (modulePath, goVersion string, requirements map[string]string, hasToolchain, hasReplace bool) {
	requirements = make(map[string]string)
	inRequireBlock := false

	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "//" {
			continue
		}

		if inRequireBlock {
			if fields[0] == ")" {
				inRequireBlock = false
				continue
			}
			if len(fields) >= 2 {
				requirements[fields[0]] = fields[1]
			}
			continue
		}

		switch fields[0] {
		case "module":
			if len(fields) >= 2 {
				modulePath = fields[1]
			}
		case "go":
			if len(fields) >= 2 {
				goVersion = fields[1]
			}
		case "require":
			if len(fields) == 2 && fields[1] == "(" {
				inRequireBlock = true
			} else if len(fields) >= 3 {
				requirements[fields[1]] = fields[2]
			}
		case "toolchain":
			hasToolchain = true
		case "replace":
			hasReplace = true
		}
	}

	return modulePath, goVersion, requirements, hasToolchain, hasReplace
}
