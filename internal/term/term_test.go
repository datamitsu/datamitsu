package term

import "testing"

func TestIsCI(t *testing.T) {
	t.Setenv("CI", "")
	if IsCI() {
		t.Fatal("IsCI() = true with empty CI, want false")
	}
	t.Setenv("CI", "true")
	if !IsCI() {
		t.Fatal("IsCI() = false with CI=true, want true")
	}
}

func TestDetectModeCIIsPlain(t *testing.T) {
	t.Setenv("CI", "1")
	if got := DetectMode(); got != Plain {
		t.Fatalf("DetectMode() under CI = %v, want Plain", got)
	}
}

func TestModeString(t *testing.T) {
	if Interactive.String() != "interactive" || Plain.String() != "plain" {
		t.Fatalf("unexpected Mode.String(): %q %q", Interactive, Plain)
	}
}
