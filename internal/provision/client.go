package provision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// ErrRemoteUnavailable means the selected provisioning worker cannot serve requests.
var ErrRemoteUnavailable = errors.New("provision worker unavailable")

// Client sends provisioning requests over one private worker transport.
type Client struct {
	rw         io.ReadWriteCloser
	writeMu    sync.Mutex
	mu         sync.Mutex
	pending    map[uint64]chan Response
	discarded  map[uint64]struct{}
	next       atomic.Uint64
	closed     chan struct{}
	err        error
	onProgress func(json.RawMessage)
}

var (
	selected    *Client
	selectionMu sync.RWMutex
)

// Select sets the process-wide client used by Call. Passing nil restores local dispatch.
func Select(client *Client) { selectionMu.Lock(); selected = client; selectionMu.Unlock() }

// RemoteSelected includes failed clients so a transport error can never enable local mutation.
func RemoteSelected() bool {
	selectionMu.RLock()
	defer selectionMu.RUnlock()
	return selected != nil
}

// NewClient creates a client that reads responses and progress from rw.
func NewClient(rw io.ReadWriteCloser, onProgress func(json.RawMessage)) *Client {
	c := &Client{rw: rw, pending: make(map[uint64]chan Response), discarded: make(map[uint64]struct{}), closed: make(chan struct{}), onProgress: onProgress}
	go c.readLoop()
	return c
}

// SetProgress replaces the callback used for worker progress events.
func (c *Client) SetProgress(callback func(json.RawMessage)) {
	c.mu.Lock()
	c.onProgress = callback
	c.mu.Unlock()
}

// Call dispatches an operation remotely when a worker client is selected.
func Call(ctx context.Context, operation string, payload any, result any) (bool, error) {
	selectionMu.RLock()
	client := selected
	selectionMu.RUnlock()
	if client == nil {
		return false, nil
	}
	return true, client.Call(ctx, operation, payload, result)
}

// Call correlates one request with its response and propagates caller cancellation.
func (c *Client) Call(ctx context.Context, operation string, payload any, result any) error {
	params, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal provision request: %w", err)
	}
	ch := make(chan Response, 1)
	c.writeMu.Lock()
	c.mu.Lock()
	select {
	case <-c.closed:
		err := c.err
		c.mu.Unlock()
		c.writeMu.Unlock()
		if err == nil {
			err = ErrRemoteUnavailable
		}
		return err
	default:
	}
	id := c.next.Add(1)
	c.pending[id] = ch
	c.mu.Unlock()
	err = writeFrame(c.rw, Request{Version: ProtocolVersion, ID: id, Kind: "request", Method: operation, Params: params})
	c.writeMu.Unlock()
	if err != nil {
		c.fail(err)
		return fmt.Errorf("write provision request: %w", err)
	}
	select {
	case response := <-ch:
		if response.Version != ProtocolVersion || response.ID != id {
			return ErrProtocol
		}
		if response.Error != nil {
			return response.Error
		}
		if response.Kind != "response" {
			return fmt.Errorf("%w: unexpected response kind %q", ErrProtocol, response.Kind)
		}
		if result == nil || len(response.Result) == 0 || string(response.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode provision response: %w", err)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		if _, waiting := c.pending[id]; waiting {
			delete(c.pending, id)
			c.discarded[id] = struct{}{}
		}
		c.mu.Unlock()
		c.writeMu.Lock()
		cancelID := c.next.Add(1)
		cancelErr := writeFrame(c.rw, Request{Version: ProtocolVersion, ID: cancelID, Kind: "cancel", CancelID: id})
		c.writeMu.Unlock()
		if cancelErr != nil {
			c.fail(cancelErr)
		}
		return fmt.Errorf("provision request canceled: %w", ctx.Err())
	case <-c.closed:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = ErrRemoteUnavailable
		}
		return err
	}
}

// Close closes the transport and wakes callers waiting for a response.
func (c *Client) Close() error { c.fail(io.EOF); return nil }

func (c *Client) readLoop() {
	for {
		var response Response
		if err := readFrame(c.rw, &response); err != nil {
			c.fail(err)
			return
		}
		if response.Version != ProtocolVersion {
			c.fail(fmt.Errorf("%w: unsupported version %d", ErrProtocol, response.Version))
			return
		}
		if response.Kind == "progress" {
			c.mu.Lock()
			_, active := c.pending[response.ID]
			_, canceled := c.discarded[response.ID]
			callback := c.onProgress
			c.mu.Unlock()
			if !active {
				if canceled {
					continue
				}
				c.fail(fmt.Errorf("%w: unknown progress id %d", ErrProtocol, response.ID))
				return
			}
			if callback != nil {
				callback(response.Event)
			}
			continue
		}
		if response.Kind != "response" || response.ID == 0 {
			c.fail(fmt.Errorf("%w: invalid response", ErrProtocol))
			return
		}
		c.mu.Lock()
		ch := c.pending[response.ID]
		if ch == nil {
			_, ignored := c.discarded[response.ID]
			if ignored {
				delete(c.discarded, response.ID)
				c.mu.Unlock()
				continue
			}
		}
		delete(c.pending, response.ID)
		c.mu.Unlock()
		if ch == nil {
			c.fail(fmt.Errorf("%w: unknown response id %d", ErrProtocol, response.ID))
			return
		}
		ch <- response
	}
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return
	default:
	}
	c.err = err
	close(c.closed)
	_ = c.rw.Close()
	c.mu.Unlock()
}
