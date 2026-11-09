package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// Handler executes one named provisioning operation with its JSON payload.
type Handler func(context.Context, string, json.RawMessage) (any, error)

// Serve handles requests with a background context until the transport closes.
func Serve(rw io.ReadWriteCloser, handler Handler) error {
	return ServeContext(context.Background(), rw, handler)
}

// ServeContext handles requests until the transport closes or ctx is canceled.
func ServeContext(ctx context.Context, rw io.ReadWriteCloser, handler Handler) error {
	if handler == nil {
		return errors.New("provision handler is required")
	}
	ctx, cancelAll := context.WithCancel(ctx)
	closeDone := make(chan struct{})
	defer close(closeDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = rw.Close()
		case <-closeDone:
		}
	}()
	var writeMu sync.Mutex
	write := func(value any) error { writeMu.Lock(); defer writeMu.Unlock(); return writeFrame(rw, value) }
	var workers sync.WaitGroup
	defer func() {
		cancelAll()
		workers.Wait()
	}()
	var lastID uint64
	var activeMu sync.Mutex
	active := make(map[uint64]context.CancelFunc)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("serve provisioning requests: %w", err)
		}
		var req Request
		if err := readFrame(rw, &req); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("serve provisioning requests: %w", ctx.Err())
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("serve provisioning requests: %w", err)
		}
		if req.Version != ProtocolVersion || req.ID <= lastID {
			return fmt.Errorf("%w: invalid request envelope", ErrProtocol)
		}
		lastID = req.ID
		if req.Kind == "cancel" {
			if req.CancelID == 0 || req.Method != "" || len(req.Params) != 0 {
				return fmt.Errorf("%w: invalid cancel frame", ErrProtocol)
			}
			activeMu.Lock()
			cancel := active[req.CancelID]
			activeMu.Unlock()
			if cancel != nil {
				cancel()
			}
			continue
		}
		if (req.Kind != "" && req.Kind != "request") || req.CancelID != 0 || req.Method == "" {
			return fmt.Errorf("%w: invalid request envelope", ErrProtocol)
		}
		operationCtx, cancel := context.WithCancel(ctx)
		activeMu.Lock()
		active[req.ID] = cancel
		activeMu.Unlock()
		workers.Add(1)
		go func(req Request, operationCtx context.Context, cancel context.CancelFunc) {
			defer workers.Done()
			defer cancel()
			defer func() { activeMu.Lock(); delete(active, req.ID); activeMu.Unlock() }()
			handlerCtx := context.WithValue(operationCtx, progressKey{}, func(event any) error {
				data, marshalErr := json.Marshal(event)
				if marshalErr != nil {
					return fmt.Errorf("encode provisioning progress: %w", marshalErr)
				}
				return write(Response{Version: ProtocolVersion, ID: req.ID, Kind: "progress", Event: data})
			})
			result, err := handler(handlerCtx, req.Method, req.Params)
			response := Response{Version: ProtocolVersion, ID: req.ID, Kind: "response"}
			if err != nil {
				response.Error = &RemoteError{Code: "operation_failed", Message: err.Error()}
			} else {
				if result == nil {
					result = struct{}{}
				}
				response.Result, err = json.Marshal(result)
				if err != nil {
					response.Error = &RemoteError{Code: "encoding_failed", Message: err.Error()}
				}
			}
			if err := write(response); err != nil {
				return
			}
		}(req, operationCtx, cancel)
	}
}

type progressKey struct{}

// HasProgress reports whether ctx can emit worker progress events.
func HasProgress(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(progressKey{}).(func(any) error)
	return ok
}

// Progress sends event to the worker's parent when progress reporting is available.
func Progress(ctx context.Context, event any) error {
	write, ok := ctx.Value(progressKey{}).(func(any) error)
	if !ok {
		return nil
	}
	return write(event)
}
