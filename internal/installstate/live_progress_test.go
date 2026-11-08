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
	select {
	case data := <-events:
		var event uievent.Event
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type != uievent.TypeInstall || event.Name != "probe" || event.Index != 3 || event.Total != 10 {
			t.Fatalf("first package update was not emitted: %+v", event)
		}
	default:
		t.Fatal("the initial status write suppressed live package progress")
	}
	select {
	case data := <-events:
		var event uievent.Event
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event.Msg != string(PhaseVerify) || event.Index != 0 || event.Total != 0 {
			t.Fatalf("phase transition lost its event or retained obsolete package counters: %+v", event)
		}
	default:
		t.Fatal("same-phase throttling suppressed the next phase transition")
	}
	select {
	case data := <-events:
		t.Fatalf("same-phase progress ignored event throttling: %s", data)
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
