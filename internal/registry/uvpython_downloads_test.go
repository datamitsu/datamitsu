package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPythonDownloadCatalogFallbackStaysOnPinnedRelease(t *testing.T) {
	for _, scenario := range []string{"legacy layout", "missing catalog", "forbidden catalog"} {
		t.Run(scenario, func(t *testing.T) {
			var legacyRequests atomic.Int32
			entry := UVPythonDownload{Name: "cpython", Arch: UVPythonArch{Family: "x86_64"}, OS: "linux", Libc: "gnu", Major: 3, Minor: 14, Patch: 6, URL: "https://example.invalid/python.tar.zst", SHA256: strings.Repeat("a", 64)}
			base := serveMetadata(t, func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/0.13.6/") {
					http.NotFound(w, r)
					return
				}
				if strings.Contains(r.URL.Path, "/uv-python-managed/") {
					if scenario == "forbidden catalog" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					http.NotFound(w, r)
					return
				}
				legacyRequests.Add(1)
				if scenario == "missing catalog" {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]UVPythonDownload{"release": entry})
			})
			previous := uvPythonMetadataBaseURL
			uvPythonMetadataBaseURL = base
			t.Cleanup(func() { uvPythonMetadataBaseURL = previous })
			entries, tag, err := GetUVPythonDownloads(context.Background(), "v0.13.6")
			if scenario == "legacy layout" {
				selected, ok := entries["release"]
				if err != nil || tag != "0.13.6" || !ok || selected.URL != entry.URL || selected.SHA256 != entry.SHA256 {
					t.Fatalf("pinned artifact catalog was lost or changed: tag=%q entries=%+v error=%v", tag, entries, err)
				}
				return
			}
			if err == nil || entries != nil || tag != "" {
				t.Fatalf("failed catalog discovery returned usable release metadata: tag=%q entries=%+v error=%v", tag, entries, err)
			}
			if scenario == "forbidden catalog" && legacyRequests.Load() != 0 {
				t.Fatal("forbidden pinned metadata silently fell back to another layout")
			}
		})
	}
}
