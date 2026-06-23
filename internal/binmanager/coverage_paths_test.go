package binmanager

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/syslist"
)

// cpHostBinaries wraps a single BinaryOsArchInfo as the registry layout for the
// current host OS/arch so the resolver picks it.
func cpHostBinaries(t *testing.T, info BinaryOsArchInfo) MapOfBinaries {
	t.Helper()
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("os type: %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("arch type: %v", err)
	}
	return MapOfBinaries{
		osType: {archType: {"unknown": info}},
	}
}

// cpSHA256Hex returns the lowercase hex SHA-256 of b, matching the format the
// download-verification paths expect.
func cpSHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// cpServeBytes starts a test server that returns body for every request.
func cpServeBytes(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// cpTarGzBytes builds an in-memory tar.gz from name->content entries.
func cpTarGzBytes(t *testing.T, files map[string]string) []byte {
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

func TestVerifyBinaryExtraction(t *testing.T) {
	ctx := context.Background()

	t.Run("single binary verifies", func(t *testing.T) {
		body := []byte("#!/bin/sh\necho ok\n")
		srv := cpServeBytes(t, body)
		if err := VerifyBinaryExtraction(ctx, srv.URL, cpSHA256Hex(body), BinHashTypeSHA256, BinContentTypeBinary, nil); err != nil {
			t.Fatalf("VerifyBinaryExtraction() error = %v, want nil", err)
		}
	})

	t.Run("tar.gz member verifies", func(t *testing.T) {
		archive := cpTarGzBytes(t, map[string]string{"bin/tool": "ELF-ish binary payload"})
		srv := cpServeBytes(t, archive)
		bp := "bin/tool"
		if err := VerifyBinaryExtraction(ctx, srv.URL, cpSHA256Hex(archive), BinHashTypeSHA256, BinContentTypeTarGz, &bp); err != nil {
			t.Fatalf("VerifyBinaryExtraction(tar.gz) error = %v, want nil", err)
		}
	})

	t.Run("empty hash is rejected", func(t *testing.T) {
		body := []byte("payload")
		srv := cpServeBytes(t, body)
		err := VerifyBinaryExtraction(ctx, srv.URL, "", BinHashTypeSHA256, BinContentTypeBinary, nil)
		if err == nil || !strings.Contains(err.Error(), "hash is empty") {
			t.Fatalf("VerifyBinaryExtraction(empty hash) = %v, want hash-empty error", err)
		}
	})

	t.Run("hash mismatch is rejected", func(t *testing.T) {
		body := []byte("payload")
		srv := cpServeBytes(t, body)
		bad := strings.Repeat("0", 64)
		err := VerifyBinaryExtraction(ctx, srv.URL, bad, BinHashTypeSHA256, BinContentTypeBinary, nil)
		if err == nil || !strings.Contains(err.Error(), "hash verification failed") {
			t.Fatalf("VerifyBinaryExtraction(bad hash) = %v, want hash-verification error", err)
		}
	})

	t.Run("download failure is reported", func(t *testing.T) {
		err := VerifyBinaryExtraction(ctx, "http://127.0.0.1:0/never", strings.Repeat("0", 64), BinHashTypeSHA256, BinContentTypeBinary, nil)
		if err == nil || !strings.Contains(err.Error(), "download failed") {
			t.Fatalf("VerifyBinaryExtraction(download fail) = %v, want download error", err)
		}
	})

	t.Run("extraction failure is reported", func(t *testing.T) {
		// Valid hash but the body is not a gzip stream, so extraction fails.
		body := []byte("not a gzip archive")
		srv := cpServeBytes(t, body)
		err := VerifyBinaryExtraction(ctx, srv.URL, cpSHA256Hex(body), BinHashTypeSHA256, BinContentTypeGz, nil)
		if err == nil || !strings.Contains(err.Error(), "extraction failed") {
			t.Fatalf("VerifyBinaryExtraction(bad archive) = %v, want extraction error", err)
		}
	})
}

func TestDownloadFileForVerify(t *testing.T) {
	ctx := context.Background()

	t.Run("downloads into dest dir", func(t *testing.T) {
		body := []byte("verify me")
		srv := cpServeBytes(t, body)
		dest := t.TempDir()
		path, err := DownloadFileForVerify(ctx, srv.URL, dest)
		if err != nil {
			t.Fatalf("DownloadFileForVerify() error = %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read downloaded file: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("content = %q, want %q", got, body)
		}
	})

	t.Run("http error is reported", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()
		if _, err := DownloadFileForVerify(ctx, srv.URL, t.TempDir()); err == nil {
			t.Error("DownloadFileForVerify(500) = nil, want error")
		}
	})
}

func TestDownloadFileSimple(t *testing.T) {
	ctx := context.Background()

	t.Run("writes body to dest path", func(t *testing.T) {
		body := []byte("simple body")
		srv := cpServeBytes(t, body)
		dest := filepath.Join(t.TempDir(), "out.bin")
		if err := downloadFileSimple(ctx, srv.URL, dest); err != nil {
			t.Fatalf("downloadFileSimple() error = %v", err)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read dest: %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("content = %q, want %q", got, body)
		}
	})

	t.Run("non-200 status is an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()
		err := downloadFileSimple(ctx, srv.URL, filepath.Join(t.TempDir(), "out.bin"))
		if err == nil || !strings.Contains(err.Error(), "404") {
			t.Fatalf("downloadFileSimple(404) = %v, want 404 error", err)
		}
	})

	t.Run("dest in a missing directory is an error", func(t *testing.T) {
		body := []byte("x")
		srv := cpServeBytes(t, body)
		// Parent does not exist, so os.Create fails.
		bad := filepath.Join(t.TempDir(), "missing-dir", "out.bin")
		if err := downloadFileSimple(ctx, srv.URL, bad); err == nil {
			t.Error("downloadFileSimple(bad dest) = nil, want error")
		}
	})
}

func TestDownloadAndExtractExternalArchive(t *testing.T) {
	ctx := context.Background()

	t.Run("missing hash is rejected", func(t *testing.T) {
		err := downloadAndExtractExternalArchive(ctx, "app", &ArchiveSpec{URL: "http://x", Format: BinContentTypeTarGz}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "must have hash") {
			t.Fatalf("missing hash = %v, want hash error", err)
		}
	})

	t.Run("missing format is rejected", func(t *testing.T) {
		err := downloadAndExtractExternalArchive(ctx, "app", &ArchiveSpec{URL: "http://x", Hash: strings.Repeat("0", 64)}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "must have format") {
			t.Fatalf("missing format = %v, want format error", err)
		}
	})

	t.Run("hash mismatch is rejected", func(t *testing.T) {
		archive := cpTarGzBytes(t, map[string]string{"bin/tool": "payload"})
		srv := cpServeBytes(t, archive)
		err := downloadAndExtractExternalArchive(ctx, "app",
			&ArchiveSpec{URL: srv.URL, Hash: strings.Repeat("0", 64), Format: BinContentTypeTarGz}, t.TempDir())
		if err == nil || !strings.Contains(err.Error(), "hash verification failed") {
			t.Fatalf("hash mismatch = %v, want verification error", err)
		}
	})

	t.Run("downloads verifies and extracts", func(t *testing.T) {
		archive := cpTarGzBytes(t, map[string]string{"bin/tool": "payload", "lib/data": "more"})
		srv := cpServeBytes(t, archive)
		install := t.TempDir()
		err := downloadAndExtractExternalArchive(ctx, "app",
			&ArchiveSpec{URL: srv.URL, Hash: cpSHA256Hex(archive), Format: BinContentTypeTarGz}, install)
		if err != nil {
			t.Fatalf("downloadAndExtractExternalArchive() error = %v", err)
		}
		got, err := os.ReadFile(filepath.Join(install, "bin", "tool"))
		if err != nil {
			t.Fatalf("extracted file missing: %v", err)
		}
		if string(got) != "payload" {
			t.Errorf("extracted content = %q, want %q", got, "payload")
		}
	})
}

func TestWriteFiles(t *testing.T) {
	t.Run("writes top-level and nested files", func(t *testing.T) {
		install := t.TempDir()
		files := map[string]string{
			"run.sh":            "#!/bin/sh\n",
			"conf/settings.yml": "a: 1\n",
		}
		if err := writeFiles(install, files); err != nil {
			t.Fatalf("writeFiles() error = %v", err)
		}
		for name, want := range files {
			got, err := os.ReadFile(filepath.Join(install, name))
			if err != nil {
				t.Fatalf("read %q: %v", name, err)
			}
			if string(got) != want {
				t.Errorf("file %q = %q, want %q", name, got, want)
			}
		}
	})

	t.Run("rejects path escaping install dir", func(t *testing.T) {
		install := t.TempDir()
		err := writeFiles(install, map[string]string{"../escape.sh": "nope"})
		if err == nil || !strings.Contains(err.Error(), "escapes install directory") {
			t.Fatalf("writeFiles(escape) = %v, want escape error", err)
		}
	})
}

func TestCopyDirAndCopyFile(t *testing.T) {
	t.Run("copies files, nested dirs and symlinks", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("symlink semantics differ on windows")
		}
		src := t.TempDir()
		if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "file.txt"), []byte("top"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "nested", "inner.txt"), []byte("deep"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("file.txt", filepath.Join(src, "link.txt")); err != nil {
			t.Fatal(err)
		}

		dst := filepath.Join(t.TempDir(), "copy")
		if err := copyDir(src, dst); err != nil {
			t.Fatalf("copyDir() error = %v", err)
		}
		if got, _ := os.ReadFile(filepath.Join(dst, "file.txt")); string(got) != "top" {
			t.Errorf("file.txt = %q, want top", got)
		}
		if got, _ := os.ReadFile(filepath.Join(dst, "nested", "inner.txt")); string(got) != "deep" {
			t.Errorf("nested/inner.txt = %q, want deep", got)
		}
		target, err := os.Readlink(filepath.Join(dst, "link.txt"))
		if err != nil {
			t.Fatalf("readlink: %v", err)
		}
		if target != "file.txt" {
			t.Errorf("symlink target = %q, want file.txt", target)
		}
	})

	t.Run("copyDir on missing source errors", func(t *testing.T) {
		err := copyDir(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "dst"))
		if err == nil {
			t.Error("copyDir(missing src) = nil, want error")
		}
	})

	t.Run("copyFile on missing source errors", func(t *testing.T) {
		err := copyFile(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "dst"))
		if err == nil {
			t.Error("copyFile(missing src) = nil, want error")
		}
	})

	t.Run("copyFile to a missing dest directory errors", func(t *testing.T) {
		src := filepath.Join(t.TempDir(), "src")
		if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		// dst parent does not exist
		err := copyFile(src, filepath.Join(t.TempDir(), "missing", "dst"))
		if err == nil {
			t.Error("copyFile(bad dst) = nil, want error")
		}
	})
}

func TestExtractZipToDir_Branches(t *testing.T) {
	t.Run("extracts dirs and nested files, skips traversal", func(t *testing.T) {
		dir := t.TempDir()
		zipPath := filepath.Join(dir, "a.zip")
		f, err := os.Create(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		zw := zip.NewWriter(f)
		// Explicit directory entry.
		if _, err := zw.Create("sub/"); err != nil {
			t.Fatal(err)
		}
		w, err := zw.Create("sub/file.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("hello")); err != nil {
			t.Fatal(err)
		}
		// Path traversal entry — must be skipped, not fail the whole extraction.
		ev, err := zw.Create("../evil.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ev.Write([]byte("nope")); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}

		out, err := extractZipToDir(zipPath, t.TempDir())
		if err != nil {
			t.Fatalf("extractZipToDir() error = %v", err)
		}
		if got, _ := os.ReadFile(filepath.Join(out, "sub", "file.txt")); string(got) != "hello" {
			t.Errorf("sub/file.txt = %q, want hello", got)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(out), "evil.txt")); err == nil {
			t.Error("traversal entry escaped the destination")
		}
	})

	t.Run("missing zip errors", func(t *testing.T) {
		if _, err := extractZipToDir(filepath.Join(t.TempDir(), "nope.zip"), t.TempDir()); err == nil {
			t.Error("extractZipToDir(missing) = nil, want error")
		}
	})
}

func TestResolvedBinaryInfo(t *testing.T) {
	t.Run("returns info for a registered binary", func(t *testing.T) {
		bm := New(MapOfApps{
			"tool": App{Binary: &AppConfigBinary{Binaries: cpHostBinaries(t, BinaryOsArchInfo{
				URL:         "http://example/tool",
				Hash:        strings.Repeat("a", 64),
				ContentType: BinContentTypeBinary,
			})}},
		}, nil, nil)
		info, err := bm.ResolvedBinaryInfo("tool")
		if err != nil {
			t.Fatalf("ResolvedBinaryInfo() error = %v", err)
		}
		if info.URL != "http://example/tool" {
			t.Errorf("URL = %q, want http://example/tool", info.URL)
		}
	})

	t.Run("unknown app errors", func(t *testing.T) {
		bm := New(MapOfApps{}, nil, nil)
		if _, err := bm.ResolvedBinaryInfo("nope"); err == nil {
			t.Error("ResolvedBinaryInfo(unknown) = nil, want error")
		}
	})

	t.Run("non-binary app errors", func(t *testing.T) {
		bm := New(MapOfApps{"svc": App{Uv: &AppConfigUV{PackageName: "svc", Version: "1.0.0"}}}, nil, nil)
		if _, err := bm.ResolvedBinaryInfo("svc"); err == nil {
			t.Error("ResolvedBinaryInfo(non-binary) = nil, want error")
		}
	})
}

func TestGetBundles(t *testing.T) {
	bundles := MapOfBundles{"b": {Version: "1.0", Files: map[string]string{"a.txt": "x"}}}
	bm := New(MapOfApps{}, bundles, nil)
	got := bm.GetBundles()
	if len(got) != 1 || got["b"] == nil {
		t.Fatalf("GetBundles() = %v, want the registered bundle", got)
	}
}

func TestInstallBundleByName(t *testing.T) {
	t.Run("installs a registered bundle", func(t *testing.T) {
		t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
		bm := New(MapOfApps{}, MapOfBundles{
			"b": {Version: "1.0", Files: map[string]string{"a.txt": "hi"}},
		}, nil)
		if err := bm.InstallBundleByName(context.Background(), "b"); err != nil {
			t.Fatalf("InstallBundleByName() error = %v", err)
		}
		path, err := bm.ComputeBundlePath("b")
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(filepath.Join(path, "a.txt")); string(got) != "hi" {
			t.Errorf("a.txt = %q, want hi", got)
		}
	})

	t.Run("unknown bundle errors", func(t *testing.T) {
		bm := New(MapOfApps{}, MapOfBundles{}, nil)
		if err := bm.InstallBundleByName(context.Background(), "nope"); err == nil {
			t.Error("InstallBundleByName(unknown) = nil, want error")
		}
	})
}

func TestInstallBundles_AlreadyCachedBranch(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	bm := New(MapOfApps{}, MapOfBundles{
		"local": {Version: "1.0", Files: map[string]string{"a.txt": "local"}},
		"external": {Version: "1.0", Archives: map[string]*ArchiveSpec{
			"x": {URL: "https://example/x.tar.gz", Hash: strings.Repeat("a", 64), Format: BinContentTypeTarGz},
		}},
	}, nil)

	stats, err := bm.InstallBundles(context.Background(), true)
	if err != nil {
		t.Fatalf("InstallBundles() error = %v", err)
	}
	if len(stats.Skipped) != 1 || stats.Skipped[0] != "external" {
		t.Errorf("Skipped = %v, want [external]", stats.Skipped)
	}
	if len(stats.Installed) != 1 || stats.Installed[0] != "local" {
		t.Errorf("Installed = %v, want [local]", stats.Installed)
	}

	// Re-running reports the local bundle as already cached.
	stats2, err := bm.InstallBundles(context.Background(), true)
	if err != nil {
		t.Fatalf("InstallBundles() second run error = %v", err)
	}
	if len(stats2.AlreadyCached) != 1 || stats2.AlreadyCached[0] != "local" {
		t.Errorf("AlreadyCached = %v, want [local]", stats2.AlreadyCached)
	}
}

func TestExtractArchiveToPath_Branches(t *testing.T) {
	t.Run("rejects unsupported format", func(t *testing.T) {
		tarPath := filepath.Join(t.TempDir(), "x.tar")
		if err := os.WriteFile(tarPath, []byte("ignored"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ExtractArchiveToDir(tarPath, BinContentTypeZip, t.TempDir()); err == nil {
			t.Error("ExtractArchiveToDir(zip format) = nil, want unsupported-format error")
		}
	})

	t.Run("requires tarData or archivePath", func(t *testing.T) {
		if _, err := extractArchiveToPath(t.TempDir(), nil, "", BinContentTypeTar); err == nil {
			t.Error("extractArchiveToPath(no source) = nil, want error")
		}
	})

	t.Run("missing archive file errors", func(t *testing.T) {
		if err := ExtractArchiveToDir(filepath.Join(t.TempDir(), "nope.tar"), BinContentTypeTar, t.TempDir()); err == nil {
			t.Error("ExtractArchiveToDir(missing) = nil, want error")
		}
	})

	t.Run("extracts tarData with dir, file and relative symlink", func(t *testing.T) {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		if err := tw.WriteHeader(&tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
		content := "binary"
		if err := tw.WriteHeader(&tar.Header{Name: "d/file", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: "d/link", Typeflag: tar.TypeSymlink, Linkname: "file"}); err != nil {
			t.Fatal(err)
		}
		// Absolute symlink — must be skipped.
		if err := tw.WriteHeader(&tar.Header{Name: "d/abs", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}

		dest := t.TempDir()
		if _, err := extractArchiveToPath(dest, buf.Bytes(), "", BinContentTypeTar); err != nil {
			t.Fatalf("extractArchiveToPath(tarData) error = %v", err)
		}
		if got, _ := os.ReadFile(filepath.Join(dest, "d", "file")); string(got) != content {
			t.Errorf("d/file = %q, want %q", got, content)
		}
		if target, err := os.Readlink(filepath.Join(dest, "d", "link")); err != nil || target != "file" {
			t.Errorf("d/link target = %q (err %v), want file", target, err)
		}
		if _, err := os.Lstat(filepath.Join(dest, "d", "abs")); err == nil {
			t.Error("absolute symlink was not skipped")
		}
	})
}
