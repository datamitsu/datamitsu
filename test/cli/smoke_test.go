package cli_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// TestSmokeVersion exercises the full blackbox path end-to-end: TestMain has
// already built the instrumented binary; here we run it as a subprocess with
// GOCOVERDIR set and assert it prints a version line. This is the minimal proof
// that the harness can drive the binary and collect coverage.
func TestSmokeVersion(t *testing.T) {
	bin := clitest.BuildOnce(t)

	cmd := exec.Command(bin, "version")
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+clitest.CoverDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`version` failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "version") {
		t.Fatalf("expected version output, got %q", out)
	}
}
