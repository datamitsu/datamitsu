package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/syslist"
	"github.com/datamitsu/datamitsu/internal/verifycache"
)

// vcHostOsArch returns the current host's syslist OS/arch types, skipping the
// test if the platform is not in syslist (so the registry lookup would miss).
func vcHostOsArch(t *testing.T) (syslist.OsType, syslist.ArchType) {
	t.Helper()
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Skipf("unsupported host OS: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Skipf("unsupported host arch: %v", err)
	}
	return osType, archType
}

// vcStateManager returns a StateManager backed by a temp file so Record() can
// persist without touching the real cache.
func vcStateManager(t *testing.T) *verifycache.StateManager {
	t.Helper()
	return verifycache.NewStateManager(
		&verifycache.VerifyState{Entries: map[string]verifycache.VerifyEntry{}},
		filepath.Join(t.TempDir(), "verify-state.json"),
	)
}

// vcSHA256Hex returns the lowercase hex SHA-256 of b.
func vcSHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// vcTarGz builds an in-memory tar.gz from name->content entries.
func vcTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Typeflag: tar.TypeReg,
			Mode:     0o755,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("tar write %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func vcServe(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestVerifyBinaryOrDir exercises both dispatch arms of verifyBinaryOrDir: the
// single-binary path (ExtractDir=false) and the directory path (ExtractDir=true,
// via verifyExtractDir), including the verifyExtractDir error branches.
func TestVerifyBinaryOrDir(t *testing.T) {
	ctx := context.Background()

	t.Run("single binary arm verifies", func(t *testing.T) {
		body := []byte("#!/bin/sh\necho ok\n")
		srv := vcServe(t, body)
		info := binmanager.BinaryOsArchInfo{
			URL:         srv.URL,
			Hash:        vcSHA256Hex(body),
			ContentType: binmanager.BinContentTypeBinary,
		}
		if err := verifyBinaryOrDir(ctx, info); err != nil {
			t.Fatalf("verifyBinaryOrDir(binary) error = %v, want nil", err)
		}
	})

	t.Run("explicit hashType is honored", func(t *testing.T) {
		body := []byte("payload-with-explicit-hashtype")
		srv := vcServe(t, body)
		ht := binmanager.BinHashTypeSHA256
		info := binmanager.BinaryOsArchInfo{
			URL:         srv.URL,
			Hash:        vcSHA256Hex(body),
			HashType:    &ht,
			ContentType: binmanager.BinContentTypeBinary,
		}
		if err := verifyBinaryOrDir(ctx, info); err != nil {
			t.Fatalf("verifyBinaryOrDir(explicit hashType) error = %v, want nil", err)
		}
	})

	t.Run("extract-dir arm extracts and finds entries", func(t *testing.T) {
		archive := vcTarGz(t, map[string]string{"bin/tool": "payload", "lib/data": "more"})
		srv := vcServe(t, archive)
		info := binmanager.BinaryOsArchInfo{
			URL:         srv.URL,
			Hash:        vcSHA256Hex(archive),
			ContentType: binmanager.BinContentTypeTarGz,
			ExtractDir:  true,
		}
		if err := verifyBinaryOrDir(ctx, info); err != nil {
			t.Fatalf("verifyBinaryOrDir(extractDir) error = %v, want nil", err)
		}
	})

	t.Run("extract-dir download failure is reported", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		srv.Close()
		info := binmanager.BinaryOsArchInfo{
			URL:         srv.URL,
			Hash:        strings.Repeat("0", 64),
			ContentType: binmanager.BinContentTypeTarGz,
			ExtractDir:  true,
		}
		if err := verifyBinaryOrDir(ctx, info); err == nil || !strings.Contains(err.Error(), "download failed") {
			t.Fatalf("verifyBinaryOrDir(download fail) = %v, want download error", err)
		}
	})

	t.Run("extract-dir empty hash is rejected", func(t *testing.T) {
		archive := vcTarGz(t, map[string]string{"bin/tool": "payload"})
		srv := vcServe(t, archive)
		info := binmanager.BinaryOsArchInfo{
			URL:         srv.URL,
			Hash:        "",
			ContentType: binmanager.BinContentTypeTarGz,
			ExtractDir:  true,
		}
		if err := verifyBinaryOrDir(ctx, info); err == nil || !strings.Contains(err.Error(), "hash is empty") {
			t.Fatalf("verifyBinaryOrDir(empty hash) = %v, want hash-empty error", err)
		}
	})

	t.Run("extract-dir hash mismatch is rejected", func(t *testing.T) {
		archive := vcTarGz(t, map[string]string{"bin/tool": "payload"})
		srv := vcServe(t, archive)
		info := binmanager.BinaryOsArchInfo{
			URL:         srv.URL,
			Hash:        strings.Repeat("0", 64),
			ContentType: binmanager.BinContentTypeTarGz,
			ExtractDir:  true,
		}
		if err := verifyBinaryOrDir(ctx, info); err == nil || !strings.Contains(err.Error(), "hash verification failed") {
			t.Fatalf("verifyBinaryOrDir(bad hash) = %v, want hash-verification error", err)
		}
	})

	t.Run("extract-dir extraction failure is reported", func(t *testing.T) {
		// Valid hash but the body is not a gzip stream, so extraction fails.
		body := []byte("not a gzip archive")
		srv := vcServe(t, body)
		info := binmanager.BinaryOsArchInfo{
			URL:         srv.URL,
			Hash:        vcSHA256Hex(body),
			ContentType: binmanager.BinContentTypeTarGz,
			ExtractDir:  true,
		}
		if err := verifyBinaryOrDir(ctx, info); err == nil || !strings.Contains(err.Error(), "extraction failed") {
			t.Fatalf("verifyBinaryOrDir(bad archive) = %v, want extraction error", err)
		}
	})
}

// TestRunPhase1BinaryApps exercises the binary-app verify phase end-to-end over
// an in-memory config whose single binary is served by httptest, covering the
// job collection, worker fan-out, result recording, and return aggregation.
func TestRunPhase1BinaryApps(t *testing.T) {
	ctx := context.Background()
	osType, archType := vcHostOsArch(t)

	body := []byte("#!/bin/sh\necho hi\n")
	srv := vcServe(t, body)

	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"tool": binmanager.App{
				Binary: &binmanager.AppConfigBinary{
					Version: "1.2.3",
					Binaries: binmanager.MapOfBinaries{
						osType: {archType: {"unknown": {
							URL:         srv.URL,
							Hash:        vcSHA256Hex(body),
							ContentType: binmanager.BinContentTypeBinary,
						}}},
					},
				},
			},
			// A non-binary (runtime) app is skipped by phase 1.
			"node-tool": binmanager.App{Node: &binmanager.AppConfigNode{Version: "1.0.0"}},
		},
	}

	results := runPhase1BinaryApps(ctx, cfg, 2, false, vcStateManager(t), false)
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].AppName != "tool" || results[0].Status != "ok" {
		t.Errorf("result = %+v, want tool/ok", results[0])
	}
}

// TestRunPhase5VersionChecks_Disabled covers the version-check phase's disabled
// arm: a binary app for the current platform whose VersionCheck is disabled is
// reported "skipped" without executing anything.
func TestRunPhase5VersionChecks_Disabled(t *testing.T) {
	ctx := context.Background()
	osType, archType := vcHostOsArch(t)

	cfg := &config.Config{
		Apps: binmanager.MapOfApps{
			"tool": binmanager.App{
				VersionCheck: &binmanager.AppVersionCheck{Disabled: true},
				Binary: &binmanager.AppConfigBinary{
					Version: "1.0.0",
					Binaries: binmanager.MapOfBinaries{
						osType: {archType: {"unknown": {
							URL:         "http://127.0.0.1:0/never",
							Hash:        strings.Repeat("0", 64),
							ContentType: binmanager.BinContentTypeBinary,
						}}},
					},
				},
			},
		},
	}

	bm := binmanager.New(cfg.Apps, cfg.Bundles, nil)
	results := runPhase5VersionChecks(ctx, cfg, bm, string(osType), string(archType), "unknown", false, vcStateManager(t), false)
	if len(results) != 1 || results[0].Status != "skipped" {
		t.Fatalf("results = %+v, want one skipped", results)
	}
}

// TestPrintVerifyResults covers the status-branch logic of the verify result
// printers (not the ANSI bytes — just that every status arm is reachable).
func TestPrintVerifyResults(t *testing.T) {
	t.Run("binary result statuses", func(t *testing.T) {
		for _, status := range []string{"ok", "cached", "failed"} {
			printBinaryResult(binaryVerifyResult{
				AppName: "app", Version: "1.0.0", Os: "linux", Arch: "amd64", Libc: "glibc",
				Status: status, ErrorMsg: "boom",
			})
		}
	})

	t.Run("runtime result statuses", func(t *testing.T) {
		for _, status := range []string{"ok", "cached", "failed"} {
			printRuntimeResult(runtimeVerifyResult{
				RuntimeName: "node", Os: "linux", Arch: "amd64", Libc: "glibc",
				Status: status, ErrorMsg: "boom",
			})
		}
	})

	t.Run("runtime app result statuses", func(t *testing.T) {
		for _, status := range []string{"ok", "cached", "failed"} {
			printRuntimeAppResult(runtimeAppResult{
				AppName: "slidev", Kind: "node", Version: "1.0.0",
				Status: status, ErrorMsg: "boom",
			})
		}
	})

	t.Run("version check result statuses", func(t *testing.T) {
		for _, status := range []string{"ok", "mismatch", "skipped", "cached", "parse_failed", "exec_failed"} {
			printVersionCheckResult(versionCheckResult{
				AppName: "tool", Args: []string{"--version"}, Expected: "1.0.0", Actual: "1.0.0",
				Status: status, ErrorMsg: "boom",
			})
		}
		// exec_failed with an empty ErrorMsg skips the trailing detail line.
		printVersionCheckResult(versionCheckResult{
			AppName: "tool", Args: []string{"--version"}, Status: "exec_failed",
		})
	})
}
