package clitest

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBuildOnceProducesBinary(t *testing.T) {
	bin := BuildOnce(t)
	if bin == "" {
		t.Fatal("BuildOnce returned empty path")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("built binary not on disk: %v", err)
	}

	// A second call must return the same path (guarded by sync.Once, not rebuilt).
	if bin2 := BuildOnce(t); bin2 != bin {
		t.Fatalf("BuildOnce not stable: %q != %q", bin, bin2)
	}
}

func TestBuiltBinaryPrintsVersion(t *testing.T) {
	bin := BuildOnce(t)

	cmd := exec.Command(bin, "version")
	cmd.Env = append(os.Environ(), "GOCOVERDIR="+CoverDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("`version` failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "version") {
		t.Fatalf("expected version output, got %q", out)
	}
}

func TestCoverDirIsStable(t *testing.T) {
	if a, b := CoverDir(), CoverDir(); a != b {
		t.Fatalf("CoverDir not stable: %q != %q", a, b)
	}
	if _, err := os.Stat(CoverDir()); err != nil {
		t.Fatalf("cover dir not created: %v", err)
	}
}
