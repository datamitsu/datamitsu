package binmanager

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/digest"
	"github.com/datamitsu/datamitsu/internal/syslist"
)

func TestDownloadInternalRejectsUnsafeStoreParents(t *testing.T) {
	payload := []byte("binary-payload")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	osType, _ := syslist.GetOsTypeFromString(runtime.GOOS)
	archType, _ := syslist.GetArchTypeFromString(runtime.GOARCH)
	for _, tc := range []string{"store-parent-file", "binary-parent-file", "symlink-escape"} {
		t.Run(tc, func(t *testing.T) {
			cache := filepath.Join(t.TempDir(), "cache")
			store := filepath.Join(cache, "store")
			bm := New(MapOfApps{"tool": {Required: true, Binary: &AppConfigBinary{Binaries: MapOfBinaries{osType: {archType: {"none": {URL: server.URL, Hash: digest.SHA256Of(payload).String(), ContentType: BinContentTypeBinary}}}}}}}, nil, nil)
			install, err := bm.getInstallPath("tool")
			if err != nil {
				t.Fatal(err)
			}
			if tc == "store-parent-file" {
				if err := os.MkdirAll(cache, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(store, []byte("file"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc == "binary-parent-file" {
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(store, ".bin"), []byte("file"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc == "symlink-escape" {
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(store, ".bin")); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			}
			t.Setenv("DATAMITSU_CACHE_DIR", cache)
			err = bm.downloadInternal(context.Background(), "tool")
			if err == nil {
				t.Fatal("unsafe store path accepted")
			}
			if _, statErr := os.Lstat(install); statErr == nil {
				t.Fatalf("unsafe destination created at %s", install)
			}
		})
	}
}

func TestWithInstalledBinarySourcePreservesPayloadOnReadAndCallbackFailure(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	osType, _ := syslist.GetOsTypeFromString(runtime.GOOS)
	archType, _ := syslist.GetArchTypeFromString(runtime.GOARCH)
	bm := New(MapOfApps{"tool": {Required: true, Binary: &AppConfigBinary{Binaries: MapOfBinaries{osType: {archType: {"none": {URL: "unused", Hash: "00", ContentType: BinContentTypeBinary}}}}}}}, nil, nil)
	install, err := bm.getInstallPath("tool")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(install), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(install, []byte("original"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantErr := context.DeadlineExceeded
	err = bm.WithInstalledBinarySource(context.Background(), "tool", func(path string) error {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "original" {
			t.Fatalf("payload changed: %q", got)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("callback error = %v", err)
	}
	got, err := os.ReadFile(install)
	if err != nil || string(got) != "original" {
		t.Fatalf("payload after callback: %q, %v", got, err)
	}
}

func TestDownloadInternalMalformedStoreRootDoesNotReplaceExistingPayload(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("DATAMITSU_CACHE_DIR", cache)
	if err := os.MkdirAll(filepath.Join(cache, "store", ".bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := []byte("replacement")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	osType, _ := syslist.GetOsTypeFromString(runtime.GOOS)
	archType, _ := syslist.GetArchTypeFromString(runtime.GOARCH)
	info := BinaryOsArchInfo{URL: server.URL, Hash: digest.SHA256Of(payload).String(), ContentType: BinContentTypeBinary}
	bm := New(MapOfApps{"tool": {Required: true, Binary: &AppConfigBinary{Binaries: MapOfBinaries{osType: {archType: {"none": info}}}}}}, nil, nil)
	install, _ := bm.getInstallPath("tool")
	if err := os.MkdirAll(filepath.Dir(install), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(install, []byte("preserved"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Replace the .bin parent after calculating the owned path, exercising the
	// real parent-file failure without permission changes or injected faults.
	if err := os.RemoveAll(filepath.Join(cache, "store", ".bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "store", ".bin"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := bm.downloadInternal(context.Background(), "tool")
	if err == nil || !strings.Contains(err.Error(), "resolve binary store target") {
		t.Fatalf("error = %v", err)
	}
}
