package reconciliation

import (
	"os"
	"strings"
	"testing"
)

// NFR-RECON-04: the module declares Go 1.25 as its floor (testing/synctest)
// and carries no toolchain or replace directives that would leak into
// consumers' builds.
func TestModuleCompatibilityPolicy(t *testing.T) {
	contents, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read go.mod: %v", err)
	}

	goVersion, hasToolchain, hasReplace := parseModuleCompatibilityPolicy(string(contents))
	if goVersion != "1.25" {
		t.Errorf("go version = %q, want %q", goVersion, "1.25")
	}
	if hasToolchain {
		t.Error("go.mod contains a toolchain directive")
	}
	if hasReplace {
		t.Error("go.mod contains a replace directive")
	}
}

func parseModuleCompatibilityPolicy(contents string) (goVersion string, hasToolchain, hasReplace bool) {
	for _, line := range strings.Split(contents, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "//" {
			continue
		}
		switch fields[0] {
		case "go":
			if len(fields) >= 2 {
				goVersion = fields[1]
			}
		case "toolchain":
			hasToolchain = true
		case "replace":
			hasReplace = true
		}
	}
	return goVersion, hasToolchain, hasReplace
}
