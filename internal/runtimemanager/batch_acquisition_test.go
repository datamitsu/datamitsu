package runtimemanager

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/digest"
)

func TestBatchRuntimeAcquisitionReportsVerifiedInstallAndOfflineReuse(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	t.Setenv("DATAMITSU_OFFLINE", "")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	host := New(nil)
	osType, archType, _, err := host.hostRuntimePlatform()
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := "probe"
	pin := digest.SHA256Of(payload).String()
	artifact := binmanager.BinaryOsArchInfo{URL: server.URL, Hash: pin, ContentType: binmanager.BinContentTypeBinary, BinaryPath: &binaryPath}
	manager := New(config.MapOfRuntimes{"probe": {
		Kind: config.RuntimeKindGo, Mode: config.RuntimeModeManaged, Go: &config.RuntimeConfigGo{GoVersion: "1.23"},
		Managed: &config.RuntimeConfigManaged{Binaries: binmanager.MapOfBinaries{osType: {archType: {"glibc": artifact, "musl": artifact, "unknown": artifact}}}},
	}})
	first, err := manager.InstallRuntimes(context.Background(), []string{"probe"}, 2)
	if err != nil || len(first.Downloaded) != 1 || first.Downloaded[0] != "probe" || len(first.Failed) != 0 {
		t.Fatalf("cold batch acquisition result = %+v, %v", first, err)
	}
	path, err := manager.GetRuntimePath(context.Background(), "probe")
	if err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(path)
	if err != nil || digest.SHA256Of(installed).String() != pin {
		t.Fatalf("batch installation did not retain verified artifact bytes: %v", err)
	}
	t.Setenv("DATAMITSU_OFFLINE", "1")
	second, err := manager.InstallRuntimes(context.Background(), []string{"probe"}, 2)
	if err != nil || len(second.AlreadyCached) != 1 || second.AlreadyCached[0] != "probe" || len(second.Downloaded) != 0 || len(second.Failed) != 0 {
		t.Fatalf("offline batch reuse result = %+v, %v", second, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("batch and lazy acquisition downloaded the artifact %d times, want once", requests.Load())
	}
}
