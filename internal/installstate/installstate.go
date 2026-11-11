// Package installstate exposes best-effort progress for processes waiting on an install target.
package installstate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/provision"
	"github.com/datamitsu/datamitsu/internal/storelock"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

// Phase identifies the current stage of a managed installation.
type Phase string

const (
	// PhaseDownload means artifact bytes are being fetched.
	PhaseDownload Phase = "download"
	// PhaseVerify means fetched bytes are being checked against their pin.
	PhaseVerify Phase = "verify"
	// PhaseExtract means a verified archive is being unpacked.
	PhaseExtract Phase = "extract"
	// PhaseInstall means an installer is preparing the target.
	PhaseInstall Phase = "install"
	// PhasePublish means the completed target is being made available.
	PhasePublish Phase = "publish"
)

const schema = "datamitsu.install-status/1"

type status struct {
	Schema    string    `json:"schema"`
	Owner     string    `json:"owner"`
	PID       int       `json:"pid"`
	Target    string    `json:"target"`
	Label     string    `json:"label"`
	Phase     string    `json:"phase"`
	UpdatedAt time.Time `json:"updatedAt"`
	Completed *int64    `json:"completed,omitempty"`
	Total     *int64    `json:"total,omitempty"`
	Unit      string    `json:"unit,omitempty"`
}

type (
	statusKey    struct{}
	statusWriter struct {
		path, owner, target, label, opID   string
		mu                                 sync.Mutex
		last                               time.Time
		lastEvent                          time.Time
		lastEventPhase, lastEventUnit      string
		lastEventCompleted, lastEventTotal int64
		hasEventCounters                   bool
		closed                             bool
		phase                              string
		completed, total                   *int64
		unit                               string
	}
)

func openStatusRoot(create bool) (*os.Root, error) {
	cacheRoot, err := os.OpenRoot(env.GetCachePath())
	if err != nil {
		return nil, fmt.Errorf("open cache for install status: %w", err)
	}
	if create {
		if err := cacheRoot.MkdirAll(filepath.Join("store-locks", "status"), 0o700); err != nil {
			closeErr := cacheRoot.Close()
			return nil, errors.Join(fmt.Errorf("create install status directory: %w", err), closeErr)
		}
	}
	root, err := cacheRoot.OpenRoot(filepath.Join("store-locks", "status"))
	closeErr := cacheRoot.Close()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open install status directory: %w", err), closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(fmt.Errorf("close cache after opening install status: %w", closeErr), root.Close())
	}
	return root, nil
}

// Acquire records progress while this process holds the target lock. The lock
// implementation signals only actual contention, avoiding false wait output.
func Acquire(ctx context.Context, storeRoot, target, label string) (context.Context, func(), error) {
	lockID, err := storelock.TargetIdentity(storeRoot, target)
	if err != nil {
		return nil, nil, err
	}
	path := lockID + ".json"
	if root, rootErr := openStatusRoot(true); rootErr == nil {
		_ = root.Close()
	}
	waitCtx, cancelWait := context.WithCancel(ctx)
	var waitOnce sync.Once
	waitDone := make(chan struct{})
	waiting := false
	release, err := storelock.LockWithWait(ctx, storeRoot, target, func() {
		waitOnce.Do(func() {
			waiting = true
			go func() { waitStatus(waitCtx, path, label); close(waitDone) }()
		})
	})
	cancelWait()
	if waiting {
		<-waitDone
	}
	if err != nil {
		return nil, nil, err
	}
	owner := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	writer := &statusWriter{path: path, owner: owner, target: target, label: label, opID: uievent.NextOpID("install")}
	writer.write("install", nil, nil, "", true)
	heartbeatDone := make(chan struct{})
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				writer.write("", nil, nil, "", false)
			}
		}
	}()
	var once sync.Once
	wrappedRelease := func() {
		once.Do(func() {
			writer.mu.Lock()
			writer.closed = true
			writer.mu.Unlock()
			stopHeartbeat()
			<-heartbeatDone
			_ = writer.remove()
			release()
		})
	}
	chain, _ := ctx.Value(statusKey{}).([]*statusWriter)
	chain = append(append([]*statusWriter(nil), chain...), writer)
	return context.WithValue(ctx, statusKey{}, chain), wrappedRelease, nil
}

func validPhase(phase string) bool {
	switch Phase(phase) {
	case PhaseDownload, PhaseVerify, PhaseExtract, PhaseInstall, PhasePublish:
		return true
	default:
		return false
	}
}

func waitStatus(ctx context.Context, path, label string) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	last := ""
	opID := uievent.NextOpID("install-wait")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			message := "Waiting for installation: " + label
			root, rootErr := openStatusRoot(false)
			var data []byte
			var err error
			if rootErr == nil {
				data, err = root.ReadFile(path)
				if closeErr := root.Close(); err == nil {
					err = closeErr
				}
			} else {
				err = rootErr
			}
			var current status
			if err == nil && json.Unmarshal(data, &current) == nil && current.Schema == schema && current.Owner != "" && current.PID > 0 && validPhase(current.Phase) && time.Since(current.UpdatedAt) >= 0 && time.Since(current.UpdatedAt) <= 5*time.Second {
				message += " (" + current.Phase
				if current.Completed != nil && *current.Completed >= 0 && (current.Unit == "bytes" || current.Unit == "packages") {
					if current.Total != nil && *current.Total > 0 && *current.Completed <= *current.Total {
						message += fmt.Sprintf(" %d/%d %s", *current.Completed, *current.Total, current.Unit)
					} else {
						message += fmt.Sprintf(" %d %s", *current.Completed, current.Unit)
					}
				}
				message += ")"
			}
			if message == last {
				continue
			}
			last = message
			event := uievent.Event{Type: uievent.TypeInstall, OpID: opID, TS: time.Now().UnixMilli(), Status: uievent.StatusProgress, Name: label, Msg: message}
			_ = provision.Progress(ctx, event)
			if !provision.HasProgress(ctx) {
				_, _ = fmt.Fprintln(os.Stderr, message)
			}
		}
	}
}

// Progress publishes optional counters without allowing status I/O failures to interrupt installation.
func Progress(ctx context.Context, phase Phase, completed, total int64, unit string) {
	if ctx == nil {
		return
	}
	chain, ok := ctx.Value(statusKey{}).([]*statusWriter)
	if !ok || len(chain) == 0 {
		return
	}
	writer := chain[len(chain)-1]
	writer.mu.Lock()
	closed := writer.closed
	writer.mu.Unlock()
	if closed {
		return
	}
	phaseName := string(phase)
	var phaseTransition bool
	for _, writer := range chain {
		c, t := completed, total
		writer.mu.Lock()
		transition := writer.phase != phaseName
		phaseTransition = phaseTransition || transition
		writer.mu.Unlock()
		writer.write(phaseName, &c, &t, unit, transition)
	}
	writer.mu.Lock()
	now := time.Now()
	hasCounters := unit == "bytes" || unit == "packages"
	changedCounters := hasCounters && (!writer.hasEventCounters || writer.lastEventPhase != phaseName || writer.lastEventCompleted != completed || writer.lastEventTotal != total || writer.lastEventUnit != unit)
	emit := !writer.closed && (writer.lastEvent.IsZero() || phaseTransition || changedCounters && (!writer.hasEventCounters || now.Sub(writer.lastEvent) >= time.Second))
	if emit {
		writer.lastEvent = now
		writer.lastEventPhase = phaseName
		writer.lastEventUnit = unit
		writer.lastEventCompleted = completed
		writer.lastEventTotal = total
		writer.hasEventCounters = hasCounters
	}
	writer.mu.Unlock()
	if !emit {
		return
	}
	event := uievent.Event{Type: uievent.TypeInstall, OpID: writer.opID, TS: now.UnixMilli(), Status: uievent.StatusProgress, Name: writer.label, Msg: phaseName}
	if phase == PhaseDownload {
		event.Type = uievent.TypeDownload
		if completed >= 0 {
			event.BytesDone = completed
		}
		if total > 0 {
			event.BytesTotal = total
			if completed >= 0 {
				event.Percent = int(completed * 100 / total)
			}
		}
	} else if unit == "packages" {
		if completed >= 0 {
			event.Index = int(completed)
		}
		if total > 0 {
			event.Total = int(total)
		}
	}
	_ = provision.Progress(ctx, event)
}

func (w *statusWriter) write(phase string, completed, total *int64, unit string, transition bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	now := time.Now()
	if phase != "" {
		w.phase = phase
	}
	if completed != nil {
		w.completed, w.total, w.unit = completed, total, unit
	}
	if !transition && now.Sub(w.last) < time.Second {
		return
	}
	data, err := json.Marshal(status{Schema: schema, Owner: w.owner, PID: os.Getpid(), Target: w.target, Label: w.label, Phase: w.phase, UpdatedAt: now, Completed: w.completed, Total: w.total, Unit: w.unit})
	if err != nil {
		return
	}
	root, err := openStatusRoot(true)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	tmp := w.path + ".tmp-" + w.owner
	if err := root.WriteFile(tmp, data, 0o600); err != nil {
		_ = root.Remove(tmp)
		return
	}
	if err := root.Rename(tmp, w.path); err != nil {
		_ = root.Remove(tmp)
		return
	}
	w.last = now
}

func (w *statusWriter) remove() (err error) {
	root, err := openStatusRoot(false)
	if err != nil {
		return fmt.Errorf("open installation status: %w", err)
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err := root.ReadFile(w.path)
	if err != nil {
		return fmt.Errorf("read installation status: %w", err)
	}
	var current status
	if json.Unmarshal(data, &current) == nil && current.Owner == w.owner {
		if err := root.Remove(w.path); err != nil {
			return fmt.Errorf("remove installation status: %w", err)
		}
	}
	return nil
}
