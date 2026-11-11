package installstate

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

func TestPackageProgressEmissionIsIndependentOfStatusWriteThrottle(t *testing.T) {
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	store := filepath.Join(t.TempDir(), "store")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	clientConn, serverConn := net.Pipe()
	client := provision.NewClient(clientConn, nil)
	defer func() { _ = client.Close() }()
	defer func() { _ = serverConn.Close() }()
	events := make(chan json.RawMessage, 4)
	client.SetProgress(func(event json.RawMessage) { events <- event })
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- provision.ServeContext(ctx, serverConn, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			owner, release, err := Acquire(ctx, store, "apps/probe", "probe")
			if err != nil {
				return nil, err
			}
			defer release()
			Progress(owner, PhaseInstall, -1, -1, "")
			Progress(owner, PhaseInstall, -1, -1, "")
			Progress(owner, PhaseInstall, 3, 10, "packages")
			Progress(owner, PhaseInstall, 3, 10, "packages")
			Progress(owner, PhaseInstall, 7, 10, "packages")
			Progress(owner, PhaseVerify, -1, -1, "")
			return struct{}{}, nil
		})
	}()
	var response struct{}
	if err := client.Call(ctx, "progress-probe", struct{}{}, &response); err != nil {
		t.Fatal(err)
	}
	readEvent := func() uievent.Event {
		t.Helper()
		select {
		case data := <-events:
			var event uievent.Event
			if err := json.Unmarshal(data, &event); err != nil {
				t.Fatal(err)
			}
			return event
		default:
			t.Fatal("expected progress event was not emitted")
			return uievent.Event{}
		}
	}
	initial := readEvent()
	if initial.Name != "probe" || initial.Msg != string(PhaseInstall) {
		t.Fatalf("initial installation phase was not emitted: %+v", initial)
	}
	first := readEvent()
	if first.Type != uievent.TypeInstall || first.Name != "probe" || first.Index != 3 || first.Total != 10 {
		t.Fatalf("first package update was not emitted: %+v", first)
	}
	second := readEvent()
	if second.Msg != string(PhaseVerify) || second.Index != 0 || second.Total != 0 {
		t.Fatalf("phase transition lost its event or retained obsolete package counters: %+v", second)
	}
	select {
	case data := <-events:
		t.Fatalf("duplicate or throttled progress was emitted: %s", data)
	default:
	}
	_ = client.Close()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("progress transport shutdown failed: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("progress operation did not release its ownership and transport")
	}
}
