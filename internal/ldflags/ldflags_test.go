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
