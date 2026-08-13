package ldflags

import "testing"

func TestPackageName(t *testing.T) {
	if PackageName == "" {
		t.Error("PackageName is empty")
	}
}

func TestConfigDTSFilename(t *testing.T) {
	if ConfigDTSFilename == "" {
		t.Error("ConfigDTSFilename is empty")
	}

	if ConfigDTSFilename != "config.d.ts" {
		t.Errorf("ConfigDTSFilename = %q, want %q", ConfigDTSFilename, "config.d.ts")
	}
}

// TestLocalArtifactsDefaultsOff pins the security default: `file://` artifact
// sources exist only in a build that explicitly injects the flag, so an ordinary
// `go build` (and every release) has the capability compiled out.
func TestLocalArtifactsDefaultsOff(t *testing.T) {
	if LocalArtifacts != "" {
		t.Errorf("LocalArtifacts = %q, want empty by default", LocalArtifacts)
	}
	if LocalArtifactsEnabled() {
		t.Error("LocalArtifactsEnabled() must be false unless injected at build time")
	}

	orig := LocalArtifacts
	t.Cleanup(func() { LocalArtifacts = orig })
	for _, injected := range []string{"1", "true", "yes"} {
		LocalArtifacts = injected
		if !LocalArtifactsEnabled() {
			t.Errorf("LocalArtifacts = %q should enable local artifacts", injected)
		}
	}
}
