package config

import (
	"strings"
	"testing"
)

func TestPythonCatalogRejectsMalformedInterpreterDeclarations(t *testing.T) {
	for _, scenario := range []string{"invalid version text", "empty key", "different interpreter", "missing platform", "negative version", "non-HTTP artifact"} {
		t.Run(scenario, func(t *testing.T) {
			entry := PythonDownload{
				Name: "cpython", Arch: PythonDownloadArch{Family: "x86_64"}, OS: "linux", LibC: "gnu",
				Major: 3, Minor: 14, Patch: 6, URL: "https://example.invalid/python.tar.zst",
				Hash: "sha256:" + strings.Repeat("a", 64),
			}
			key, version := "host", "3.14.6"
			switch scenario {
			case "invalid version text":
				version = "3.14.6\n"
			case "empty key":
				key = ""
			case "different interpreter":
				entry.Name = "pypy"
			case "missing platform":
				entry.Arch.Family = ""
			case "negative version":
				entry.Minor = -1
				version = "3.-1.6"
			case "non-HTTP artifact":
				entry.URL = "file:///python.tar.zst"
			}
			downloads := map[string]PythonDownload{key: entry}
			if scenario == "invalid version text" {
				downloads = nil
			}
			runtime := RuntimeConfig{Kind: RuntimeKindUV, UV: &RuntimeConfigUV{PythonVersion: version, PythonDownloads: downloads}}
			if errs := validateUVRuntimeKind("python", runtime); len(errs) == 0 {
				t.Fatalf("invalid Python catalog accepted: %+v", runtime.UV)
			}
		})
	}
}
