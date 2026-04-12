package color

import (
	"os"
	"testing"

	fatihcolor "github.com/fatih/color"
)

// saveEnv saves and clears all color-related env vars, returns a restore function
func saveEnv(t *testing.T) func() {
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
	return func() {
		for _, v := range vars {
			s := saved[v]
			if s.set {
				_ = os.Setenv(v, s.value)
			} else {
				_ = os.Unsetenv(v)
			}
		}
	}
}

func TestEnabledNoColor(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("NO_COLOR", "")
	if Enabled() {
		t.Error("NO_COLOR set (even empty) should disable colors")
	}

	_ = os.Setenv("NO_COLOR", "1")
	if Enabled() {
		t.Error("NO_COLOR=1 should disable colors")
	}

	_ = os.Setenv("NO_COLOR", "anything")
	if Enabled() {
		t.Error("NO_COLOR=anything should disable colors")
	}
}

func TestEnabledForceColor(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("FORCE_COLOR", "1")
	if !Enabled() {
		t.Error("FORCE_COLOR=1 should enable colors")
	}

	_ = os.Setenv("FORCE_COLOR", "true")
	if !Enabled() {
		t.Error("FORCE_COLOR=true should enable colors")
	}

	_ = os.Setenv("FORCE_COLOR", "0")
	// FORCE_COLOR=0 should not force enable
	// Falls through to terminal detection (which may be false in test)
}

func TestEnabledForceColorZero(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("FORCE_COLOR", "0")
	// FORCE_COLOR=0 is treated as "not forcing", falls through to TTY detection.
	// In tests stdout is a pipe (not TTY), so Enabled() returns false.
	if Enabled() {
		t.Error("FORCE_COLOR=0 should not force-enable colors (falls through to TTY detection)")
	}
}

func TestEnabledNoColorTakesPrecedenceOverForceColor(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("NO_COLOR", "1")
	_ = os.Setenv("FORCE_COLOR", "1")
	if Enabled() {
		t.Error("NO_COLOR should take precedence over FORCE_COLOR")
	}
}

func TestEnabledCLICOLORForce(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("CLICOLOR_FORCE", "1")
	if !Enabled() {
		t.Error("CLICOLOR_FORCE=1 should enable colors")
	}
}

func TestEnabledCLICOLORDisable(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("CLICOLOR", "0")
	if Enabled() {
		t.Error("CLICOLOR=0 should disable colors")
	}
}

func TestEnabledCINoTTY(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	// In test environment, stdout is a pipe (not a TTY).
	// Without any color env vars set, Enabled() should return false.
	if Enabled() {
		t.Error("expected Enabled()=false when stdout is not a TTY and no color env vars are set")
	}
}

func TestEnabledCLICOLORForceOverridesCLICOLOR(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("CLICOLOR", "0")
	_ = os.Setenv("CLICOLOR_FORCE", "1")
	if !Enabled() {
		t.Error("CLICOLOR_FORCE should override CLICOLOR=0")
	}
}

func TestChildEnvHintsWhenDisabled(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("NO_COLOR", "1")
	hints := ChildEnvHints()
	if hints != nil {
		t.Errorf("ChildEnvHints should return nil when colors disabled, got %v", hints)
	}
}

func TestChildEnvHintsWhenEnabled(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("FORCE_COLOR", "1")
	hints := ChildEnvHints()
	if hints == nil {
		t.Fatal("ChildEnvHints should return hints when colors enabled")
	}
	if hints["FORCE_COLOR"] != "" {
		t.Error("ChildEnvHints should not override user-set FORCE_COLOR")
	}
	if _, ok := hints["CLICOLOR_FORCE"]; !ok {
		t.Error("ChildEnvHints should set CLICOLOR_FORCE when not set by user")
	}
}

func TestChildEnvHintsRespectsUserEnv(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("CLICOLOR_FORCE", "1")
	hints := ChildEnvHints()
	if hints == nil {
		t.Fatal("ChildEnvHints should return hints when colors enabled")
	}

	// User already set CLICOLOR_FORCE, so it should not be in hints
	if _, ok := hints["CLICOLOR_FORCE"]; ok {
		t.Error("ChildEnvHints should not override user-set CLICOLOR_FORCE")
	}

	// FORCE_COLOR was not set by user, so it should be in hints
	if v, ok := hints["FORCE_COLOR"]; !ok || v != "1" {
		t.Errorf("ChildEnvHints should set FORCE_COLOR=1 when not set by user, got %q", v)
	}
}

func TestChildEnvHintsNoOverrideUserVars(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	// User explicitly set both vars
	_ = os.Setenv("FORCE_COLOR", "1")
	_ = os.Setenv("CLICOLOR_FORCE", "1")

	hints := ChildEnvHints()
	if hints == nil {
		t.Fatal("ChildEnvHints should return non-nil (even if empty) when colors enabled")
	}

	// Neither should be in hints since user already set both
	if _, ok := hints["FORCE_COLOR"]; ok {
		t.Error("should not override user-set FORCE_COLOR")
	}
	if _, ok := hints["CLICOLOR_FORCE"]; ok {
		t.Error("should not override user-set CLICOLOR_FORCE")
	}
}

func TestInit(t *testing.T) {
	restore := saveEnv(t)
	defer restore()

	_ = os.Setenv("NO_COLOR", "1")
	Init()
	if !fatihcolor.NoColor {
		t.Error("Init() with NO_COLOR=1 should set fatihcolor.NoColor=true")
	}

	_ = os.Unsetenv("NO_COLOR")
	_ = os.Setenv("FORCE_COLOR", "1")
	Init()
	if fatihcolor.NoColor {
		t.Error("Init() with FORCE_COLOR=1 should set fatihcolor.NoColor=false")
	}
}
