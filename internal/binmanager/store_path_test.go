package binmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

const (
	helperProcessEnv    = "BINMANAGER_TEST_HELPER_PROCESS"
	helperProcessMarker = "binmanager helper process ran"
)

func TestBinaryStorePath(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())

	tests := []struct {
		goos       string
		extractDir bool
		want       string
	}{
		{goos: "linux", want: "abc123"},
		{goos: "darwin", want: "abc123"},
		{goos: "windows", want: "abc123.exe"},
		{goos: "linux", extractDir: true, want: "abc123"},
		{goos: "windows", extractDir: true, want: "abc123"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s/extractDir=%v", tt.goos, tt.extractDir), func(t *testing.T) {
			got := binaryStorePath("tool", "abc123", tt.extractDir, tt.goos)
			if filepath.Base(got) != tt.want {
				t.Errorf("file name = %q, want %q", filepath.Base(got), tt.want)
			}
			if filepath.Base(filepath.Dir(got)) != "tool" {
				t.Errorf("parent = %q, want the app directory %q", filepath.Dir(got), "tool")
			}
		})
	}
}

// TestBinaryAppHelperProcess is not a test: TestExecDownloadedBinary downloads this test binary as
// a binary app and runs it with helperProcessEnv set, which lands here.
func TestBinaryAppHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		t.Skip("helper process for TestExecDownloadedBinary")
	}
	fmt.Println(helperProcessMarker)
	os.Exit(0)
}

// TestExecDownloadedBinary installs a real executable of the host platform as a binary app and
// runs it through the same path every tool invocation takes. The fixtures elsewhere serve shell
// scripts, which only a POSIX host can execute, so on Windows nothing else starts a binary app.
func TestExecDownloadedBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() = %v", err)
	}
	content, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	sum := sha256.Sum256(content)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	defer server.Close()

	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv(helperProcessEnv, "1")

	osType, err := syslist.GetOsTypeFromString(runtime.GOOS)
	if err != nil {
		t.Fatalf("GetOsTypeFromString() = %v", err)
	}
	archType, err := syslist.GetArchTypeFromString(runtime.GOARCH)
	if err != nil {
		t.Fatalf("GetArchTypeFromString() = %v", err)
	}
	libc := string(target.DetectHost(context.Background()).Libc)

	bm := New(MapOfApps{
		"helper": App{
			Required: true,
			Binary: &AppConfigBinary{
				Binaries: MapOfBinaries{
					osType: {
						archType: {libc: BinaryOsArchInfo{
							URL:         server.URL,
							Hash:        hex.EncodeToString(sum[:]),
							ContentType: BinContentTypeBinary,
						}},
					},
				},
			},
		},
	}, nil, nil)

	out, err := bm.ExecCaptured(context.Background(), "helper", []string{"-test.run=^TestBinaryAppHelperProcess$"})
	if err != nil {
		t.Fatalf("ExecCaptured() = %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, helperProcessMarker) {
		t.Errorf("output = %q, want it to contain %q", out, helperProcessMarker)
	}
}
