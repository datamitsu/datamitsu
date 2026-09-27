package color

import (
	"os"
	"testing"

	fatihcolor "github.com/fatih/color"
)

// saveEnv clears all color-related env vars so each test starts from a known
// state, and registers a cleanup that restores their original host values.
// Cleanup is registered before any test-body t.Setenv call, so it runs last
// (t.Cleanup is LIFO) and the host values win.
func saveEnv(t *testing.T) {
	t.Helper()
	vars := []string{"NO_COLOR", "FORCE_COLOR", "CLICOLOR_FORCE", "CLICOLOR"}
	saved := make(map[string]struct {
		value string
		set   bool
	})
	for _, v := range vars {
		val, ok := os.LookupEnv(v)
		saved[v] = struct {
			value string
			set   bool
		}{val, ok}
		_ = os.Unsetenv(v)
	}
	t.Cleanup(func() {
		for _, v := range vars {
			s := saved[v]
			if s.set {
				_ = os.Setenv(v, s.value) //nolint:usetesting // explicit restore of original host value paired with os.Unsetenv branch
			} else {
				_ = os.Unsetenv(v)
			}
		}
	})
}

func TestEnabledNoColor(t *testing.T) {
	saveEnv(t)

	t.Setenv("NO_COLOR", "")
	if Enabled() {
		t.Error("NO_COLOR set (even empty) should disable colors")
	}

	t.Setenv("NO_COLOR", "1")
	if Enabled() {
		t.Error("NO_COLOR=1 should disable colors")
	}

	t.Setenv("NO_COLOR", "anything")
	if Enabled() {
		t.Error("NO_COLOR=anything should disable colors")
	}
}

func TestEnabledForceColor(t *testing.T) {
	saveEnv(t)

	t.Setenv("FORCE_COLOR", "1")
	if !Enabled() {
		t.Error("FORCE_COLOR=1 should enable colors")
	}

	t.Setenv("FORCE_COLOR", "true")
	if !Enabled() {
		t.Error("FORCE_COLOR=true should enable colors")
	}

	t.Setenv("FORCE_COLOR", "0")
	// FORCE_COLOR=0 should not force enable
	// Falls through to terminal detection (which may be false in test)
}

func TestEnabledForceColorZero(t *testing.T) {
	saveEnv(t)

	t.Setenv("FORCE_COLOR", "0")
	// FORCE_COLOR=0 is treated as "not forcing", falls through to TTY detection.
	// In tests stdout is a pipe (not TTY), so Enabled() returns false.
	if Enabled() {
		t.Error("FORCE_COLOR=0 should not force-enable colors (falls through to TTY detection)")
	}
}

func TestEnabledNoColorTakesPrecedenceOverForceColor(t *testing.T) {
	saveEnv(t)

	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "1")
	if Enabled() {
		t.Error("NO_COLOR should take precedence over FORCE_COLOR")
	}
}

func TestEnabledCLICOLORForce(t *testing.T) {
	saveEnv(t)

	t.Setenv("CLICOLOR_FORCE", "1")
	if !Enabled() {
		t.Error("CLICOLOR_FORCE=1 should enable colors")
	}
}

func TestEnabledCLICOLORDisable(t *testing.T) {
	saveEnv(t)

	t.Setenv("CLICOLOR", "0")
	if Enabled() {
		t.Error("CLICOLOR=0 should disable colors")
	}
}

func TestEnabledCINoTTY(t *testing.T) {
	saveEnv(t)

	// In test environment, stdout is a pipe (not a TTY).
	// Without any color env vars set, Enabled() should return false.
	if Enabled() {
		t.Error("expected Enabled()=false when stdout is not a TTY and no color env vars are set")
	}
}

func TestEnabledCLICOLORForceOverridesCLICOLOR(t *testing.T) {
	saveEnv(t)

	t.Setenv("CLICOLOR", "0")
	t.Setenv("CLICOLOR_FORCE", "1")
	if !Enabled() {
		t.Error("CLICOLOR_FORCE should override CLICOLOR=0")
	}
}

func TestInit(t *testing.T) {
	saveEnv(t)

	t.Setenv("NO_COLOR", "1")
	Init()
	if !fatihcolor.NoColor {
		t.Error("Init() with NO_COLOR=1 should set fatihcolor.NoColor=true")
	}

	_ = os.Unsetenv("NO_COLOR")
	t.Setenv("FORCE_COLOR", "1")
	Init()
	if fatihcolor.NoColor {
		t.Error("Init() with FORCE_COLOR=1 should set fatihcolor.NoColor=false")
	}
}
