package config

import (
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
)

// appWithName builds a minimal, otherwise-valid app map keyed by name, so a
// failing validation can only be about the name itself.
func appWithName(names ...string) binmanager.MapOfApps {
	apps := make(binmanager.MapOfApps, len(names))
	for _, n := range names {
		apps[n] = binmanager.App{Required: true}
	}
	return apps
}

func TestValidateApps_HostileAppNames(t *testing.T) {
	tests := []struct {
		name    string
		appName string
		wantMsg string
	}{
		{name: "parent traversal", appName: "../x", wantMsg: "path separator"},
		{name: "absolute", appName: "/etc/passwd", wantMsg: "path separator"},
		{name: "backslash", appName: `a\b`, wantMsg: "path separator"},
		{name: "space", appName: "a b", wantMsg: "must match"},
		{name: "command substitution", appName: "$(id)", wantMsg: "must match"},
		{name: "newline", appName: "a\nb", wantMsg: "must match"},
		{name: "dot", appName: ".", wantMsg: "directory reference"},
		{name: "dotdot", appName: "..", wantMsg: "directory reference"},
		{name: "leading hyphen", appName: "-rf", wantMsg: "must not start with a hyphen"},
		{name: "too long", appName: strings.Repeat("a", 300), wantMsg: "must match"},
		{name: "empty", appName: "", wantMsg: "app name must not be empty"},
		{name: "windows CON", appName: "CON", wantMsg: "reserved device name"},
		{name: "windows com1", appName: "com1", wantMsg: "reserved device name"},
		{name: "windows stem with extension", appName: "nul.exe", wantMsg: "reserved device name"},
		{name: "leading dot", appName: ".hidden", wantMsg: "must match"},
		{name: "trailing null byte", appName: "a\x00b", wantMsg: "must match"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateApps(appWithName(tt.appName), nil)
			if err == nil {
				t.Fatalf("ValidateApps(%q) = nil, want error", tt.appName)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("ValidateApps(%q) error = %q, want it to contain %q", tt.appName, err, tt.wantMsg)
			}
			if !strings.Contains(err.Error(), "filesystem entries") {
				t.Errorf("ValidateApps(%q) error = %q, want it to explain the filesystem constraint", tt.appName, err)
			}
		})
	}
}

func TestValidateApps_ValidAppNames(t *testing.T) {
	valid := []string{
		"yq-json", "terraform-docs", "mmdc", "git-cliff", "task",
		"golangci-lint", "d2", "age", "editorconfig-checker", "ast-grep",
		"a", "A1", "tool_v2", "node.js-tool", strings.Repeat("a", 64),
	}

	for _, name := range valid {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateApps(appWithName(name), nil); err != nil {
				t.Errorf("ValidateApps(%q) unexpected error: %v", name, err)
			}
		})
	}
}

func TestValidateApps_CaseFoldCollision(t *testing.T) {
	apps := appWithName("Git", "git")

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() = nil, want a case-fold collision error")
	}

	msg := err.Error()
	if got := strings.Count(msg, "differ only in case"); got != 1 {
		t.Errorf("got %d case-fold errors, want exactly 1; error: %s", got, msg)
	}
	if !strings.Contains(msg, `"Git"`) || !strings.Contains(msg, `"git"`) {
		t.Errorf("error %q must name both colliding names", msg)
	}
}

func TestValidateApps_CaseFoldCollisionThreeWay(t *testing.T) {
	// Three variants of one name are two collisions against the first-sorted
	// name, not three pairwise ones — the message is about which file wins.
	_, err := ValidateApps(appWithName("Tool", "TOOL", "tool"), nil)
	if err == nil {
		t.Fatal("ValidateApps() = nil, want a case-fold collision error")
	}
	if got := strings.Count(err.Error(), "differ only in case"); got != 2 {
		t.Errorf("got %d case-fold errors, want 2; error: %s", got, err)
	}
}

func TestValidateApps_DistinctNamesNoCollision(t *testing.T) {
	if _, err := ValidateApps(appWithName("git-cliff", "gitleaks", "task"), nil); err != nil {
		t.Errorf("ValidateApps() unexpected error: %v", err)
	}
}

// TestDefaultConfigAppNamesValid guards the shipped config: every app the
// embedded default declares must satisfy the rule this file introduces, or
// `datamitsu` fails to validate its own defaults.
func TestDefaultConfigAppNamesValid(t *testing.T) {
	cfg := runDefaultConfigForTest(t)

	if len(cfg.Apps) == 0 {
		t.Fatal("embedded default config declares no apps")
	}

	for name := range cfg.Apps {
		if err := validateAppName(name); err != nil {
			t.Errorf("embedded default config app %q: %v", name, err)
		}
	}

	names := make([]string, 0, len(cfg.Apps))
	for name := range cfg.Apps {
		names = append(names, name)
	}
	if err := ValidateAppNames(names); err != nil {
		t.Errorf("embedded default config app names: %v", err)
	}
}

func TestValidateAppNames(t *testing.T) {
	if err := ValidateAppNames([]string{"task", "mmdc"}); err != nil {
		t.Errorf("ValidateAppNames() unexpected error: %v", err)
	}
	if err := ValidateAppNames([]string{"Task", "task"}); err == nil {
		t.Error("ValidateAppNames() = nil for a case-fold collision, want error")
	}
	if err := ValidateAppNames([]string{"../x"}); err == nil {
		t.Error("ValidateAppNames() = nil for a traversal name, want error")
	}
	if err := ValidateAppNames(nil); err != nil {
		t.Errorf("ValidateAppNames(nil) unexpected error: %v", err)
	}
}

// TestValidateApps_BadNameSuppressesPerKindErrors documents that an unusable
// name short-circuits the rest of that app's checks: the name is the thing to
// fix first, and its per-kind errors would be about an app that cannot exist.
func TestValidateApps_BadNameSuppressesPerKindErrors(t *testing.T) {
	apps := binmanager.MapOfApps{
		"../evil": {
			Jvm: &binmanager.AppConfigJVM{}, // missing jarUrl, jarHash and version
		},
	}

	_, err := ValidateApps(apps, nil)
	if err == nil {
		t.Fatal("ValidateApps() = nil, want error")
	}
	if strings.Contains(err.Error(), "jvm.jarUrl") {
		t.Errorf("error %q should report only the name problem", err)
	}
}
