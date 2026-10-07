package binmanager

import (
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
	"github.com/datamitsu/datamitsu/internal/target"
)

// extractDirFixture serves archive as the host platform's build of the app "tool", whose entry
// extracts the whole tree and runs binaryPath inside it.
func extractDirFixture(t *testing.T, archive string, contentType BinContentType, binaryPath string) *BinManager {
	t.Helper()
	content, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	t.Cleanup(server.Close)

	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	libc := string(target.DetectHost(context.Background()).Libc)

	binary := &AppConfigBinary{Binaries: MapOfBinaries{
		osType: {archType: {libc: BinaryOsArchInfo{
			URL:         server.URL,
			Hash:        hex.EncodeToString(sum[:]),
			ContentType: contentType,
			BinaryPath:  &binaryPath,
			ExtractDir:  true,
		}}},
	}}
	return New(MapOfApps{
		"tool": {Required: true, Binary: binary},
		"user": {Shell: &AppConfigShell{Name: "sh"}, DependsOn: []string{"tool"}},
	}, nil, nil)
}

// The fixture command prints the file beside it, the way protoc reads its include/ relative
// to its own binary: only a tree installed whole can answer.
const extractDirScript = "#!/bin/sh\ncat \"$(dirname \"$0\")/../include/x.proto\"\n"

func TestExtractDirBinaryAppRunsBinaryPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture command is a shell script")
	}
	entries := []archiveEntry{
		{name: "tool-1.0/bin/tool", content: extractDirScript, mode: 0o755},
		{name: "tool-1.0/include/x.proto", content: "syntax = \"proto3\";\n", mode: 0o644},
		{name: "tool-1.0/readme.txt", content: "readme\n", mode: 0o644},
	}
	tests := []struct {
		name        string
		contentType BinContentType
		write       func(t *testing.T, path string, entries []archiveEntry)
		entries     []archiveEntry
	}{
		{"tar.gz", BinContentTypeTarGz, writeTarGzWithModes, entries},
		// A zip built on Windows carries no modes; the command is made executable on install.
		{"zip without modes", BinContentTypeZip, writeZipWithModes, []archiveEntry{
			{name: "tool-1.0/bin/tool", content: extractDirScript, mode: 0o644},
			{name: "tool-1.0/include/x.proto", content: "syntax = \"proto3\";\n", mode: 0o644},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "tool."+string(tt.contentType))
			tt.write(t, archive, tt.entries)
			bm := extractDirFixture(t, archive, tt.contentType, "tool-1.0/bin/tool")
			ctx := context.Background()

			out, err := bm.ExecCaptured(ctx, "tool", nil)
			if err != nil {
				t.Fatalf("ExecCaptured() = %v\noutput:\n%s", err, out)
			}
			if !strings.Contains(out, "proto3") {
				t.Errorf("output = %q, want the include file read beside the binary", out)
			}

			installRoot, err := bm.ComputeInstallPath("tool")
			if err != nil {
				t.Fatal(err)
			}
			command, err := bm.getBinaryPath("tool")
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(installRoot, "tool-1.0", "bin", "tool"); command != want {
				t.Errorf("getBinaryPath() = %q, want %q", command, want)
			}
			if _, err := os.Stat(filepath.Join(installRoot, "tool-1.0", "include", "x.proto")); err != nil {
				t.Errorf("the rest of the tree is not installed: %v", err)
			}

			info, installed, err := bm.ResolveCommandInfo("tool")
			if err != nil {
				t.Fatal(err)
			}
			if !installed || info.Command != command {
				t.Errorf("ResolveCommandInfo() = (%q, installed=%v), want (%q, true)", info.Command, installed, command)
			}

			// A dependent finds the command, not the directory, on its PATH.
			userInfo, err := bm.getCommandInfo(ctx, "user")
			if err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(pathHead(t, userInfo), "tool")
			if resolved, err := filepath.EvalSymlinks(link); err != nil || resolved != mustEvalSymlinks(t, command) {
				t.Errorf("dependency PATH entry %q resolves to (%q, %v), want %q", link, resolved, err, command)
			}
		})
	}
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// A binaryPath the archive does not hold is a configuration error: the install fails naming
// it, and nothing is left in the store to be skipped over on the next attempt.
func TestExtractDirBinaryAppMissingBinaryPathFails(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "tool.tar.gz")
	writeTarGzWithModes(t, archive, []archiveEntry{
		{name: "tool-1.0/bin/tool", content: extractDirScript, mode: 0o755},
	})
	bm := extractDirFixture(t, archive, BinContentTypeTarGz, "bin/tool")

	_, err := bm.getOrInstallBinaryPath(context.Background(), "tool")
	if err == nil || !strings.Contains(err.Error(), `binaryPath "bin/tool" not found`) {
		t.Fatalf("getOrInstallBinaryPath() = %v, want the missing binaryPath named", err)
	}
	installRoot, err := bm.ComputeInstallPath("tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installRoot); !os.IsNotExist(err) {
		t.Errorf("install root %q left behind after a failed install (stat: %v)", installRoot, err)
	}
	if _, installed, err := bm.ResolveCommandInfo("tool"); err != nil || installed {
		t.Errorf("ResolveCommandInfo() installed = %v, err = %v; want not installed", installed, err)
	}
}

// An emptied installation directory — the runtime manager left one behind when it moved a
// runtime into its own cache — gives way to the fresh extraction. A directory that still holds
// something but not the command is never deleted: the install fails naming it.
func TestExtractDirBinaryAppInstalledDirectoryWithoutCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fixture command is a shell script")
	}
	archive := filepath.Join(t.TempDir(), "tool.tar.gz")
	writeTarGzWithModes(t, archive, []archiveEntry{
		{name: "tool-1.0/bin/tool", content: extractDirScript, mode: 0o755},
		{name: "tool-1.0/include/x.proto", content: "syntax = \"proto3\";\n", mode: 0o644},
	})

	t.Run("empty directory is replaced", func(t *testing.T) {
		bm := extractDirFixture(t, archive, BinContentTypeTarGz, "tool-1.0/bin/tool")
		installRoot, err := bm.ComputeInstallPath("tool")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(installRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		out, err := bm.ExecCaptured(context.Background(), "tool", nil)
		if err != nil {
			t.Fatalf("ExecCaptured() = %v\noutput:\n%s", err, out)
		}
	})

	t.Run("non-empty directory is kept and named", func(t *testing.T) {
		bm := extractDirFixture(t, archive, BinContentTypeTarGz, "tool-1.0/bin/tool")
		installRoot, err := bm.ComputeInstallPath("tool")
		if err != nil {
			t.Fatal(err)
		}
		leftover := filepath.Join(installRoot, "leftover")
		if err := os.MkdirAll(leftover, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err = bm.getOrInstallBinaryPath(context.Background(), "tool")
		if err == nil || !strings.Contains(err.Error(), installRoot) || !strings.Contains(err.Error(), "remove the directory") {
			t.Fatalf("getOrInstallBinaryPath() = %v, want the directory named", err)
		}
		if _, err := os.Stat(leftover); err != nil {
			t.Errorf("the directory's content was removed: %v", err)
		}
	})
}

func TestBinaryCommandPath(t *testing.T) {
	install := filepath.Join("store", ".bin", "tool", "abc123")
	tests := []struct {
		name    string
		info    BinaryOsArchInfo
		want    string
		wantErr bool
	}{
		{"single file", BinaryOsArchInfo{BinaryPath: new("bin/tool")}, install, false},
		{"extractDir without binaryPath", BinaryOsArchInfo{ExtractDir: true}, install, false},
		{"extractDir", BinaryOsArchInfo{ExtractDir: true, BinaryPath: new("tool-1.0/bin/tool")}, filepath.Join(install, "tool-1.0", "bin", "tool"), false},
		{"extractDir dot-prefixed", BinaryOsArchInfo{ExtractDir: true, BinaryPath: new("./tool")}, filepath.Join(install, "tool"), false},
		{"extractDir escaping", BinaryOsArchInfo{ExtractDir: true, BinaryPath: new("../other/tool")}, "", true},
		{"extractDir directory itself", BinaryOsArchInfo{ExtractDir: true, BinaryPath: new(".")}, "", true},
		{"extractDir cleans to directory itself", BinaryOsArchInfo{ExtractDir: true, BinaryPath: new("bin/..")}, "", true},
		{"extractDir absolute", BinaryOsArchInfo{ExtractDir: true, BinaryPath: new(string(filepath.Separator) + "tool")}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := binaryCommandPath(install, tt.info)
			if (err != nil) != tt.wantErr {
				t.Fatalf("binaryCommandPath() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("binaryCommandPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckExtractedCommand(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin", "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "tool"), []byte(extractDirScript), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := checkExtractedCommand(dir, BinaryOsArchInfo{ExtractDir: true, BinaryPath: new("bin/tool")}); err != nil {
		t.Fatalf("checkExtractedCommand(present) = %v", err)
	}
	if runtime.GOOS != "windows" {
		stat, err := os.Stat(filepath.Join(dir, "bin", "tool"))
		if err != nil {
			t.Fatal(err)
		}
		if stat.Mode().Perm()&0o111 == 0 {
			t.Errorf("command mode = %v, want it executable", stat.Mode())
		}
	}
	if err := checkExtractedCommand(dir, BinaryOsArchInfo{ExtractDir: true, BinaryPath: new("bin/missing")}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("checkExtractedCommand(missing) = %v, want not-found error", err)
	}
	if err := checkExtractedCommand(dir, BinaryOsArchInfo{ExtractDir: true, BinaryPath: new("bin/subdir")}); err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("checkExtractedCommand(directory) = %v, want directory error", err)
	}
	if err := checkExtractedCommand(dir, BinaryOsArchInfo{ExtractDir: true}); err != nil {
		t.Errorf("checkExtractedCommand(no binaryPath) = %v, want nil", err)
	}
	if err := checkExtractedCommand(dir, BinaryOsArchInfo{ExtractDir: true, BinaryPath: new("bin/..")}); err == nil || !strings.Contains(err.Error(), "directory itself") {
		t.Errorf("checkExtractedCommand(directory itself) = %v, want the directory named", err)
	}
}

func TestIsDirectoryArchive(t *testing.T) {
	for _, ct := range []BinContentType{BinContentTypeTarGz, BinContentTypeTarBz2, BinContentTypeTarXz, BinContentTypeTarZst, BinContentTypeTar, BinContentTypeZip} {
		if !ct.IsDirectoryArchive() {
			t.Errorf("%s.IsDirectoryArchive() = false, want true", ct)
		}
	}
	for _, ct := range []BinContentType{BinContentTypeBinary, BinContentTypeGz, BinContentTypeBz2, BinContentTypeXz, BinContentTypeZst, BinContentType("")} {
		if ct.IsDirectoryArchive() {
			t.Errorf("%q.IsDirectoryArchive() = true, want false", ct)
		}
	}
}
