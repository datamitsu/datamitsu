package provision

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestReadFrameRejectsMalformedFrames(t *testing.T) {
	for _, tt := range []struct {
		name string
		data []byte
	}{
		{"empty", []byte{0, 0, 0, 0}},
		{"invalid JSON", framed([]byte("{"))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var req Request
			if err := readFrame(strings.NewReader(string(tt.data)), &req); !errors.Is(err, ErrProtocol) {
				t.Fatalf("readFrame error = %v, want ErrProtocol", err)
			}
		})
	}
}

func TestServeRejectsInvalidRequestEnvelope(t *testing.T) {
	for _, tt := range []struct {
		name string
		req  Request
	}{
		{"version", Request{Version: ProtocolVersion + 1, ID: 1, Method: "run"}},
		{"id", Request{Version: ProtocolVersion, Method: "run"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client, server := net.Pipe()
			defer func() { _ = client.Close() }()
			go func() { _ = writeFrame(client, tt.req) }()
			err := Serve(server, func(context.Context, string, json.RawMessage) (any, error) {
				t.Fatal("handler called for invalid request")
				return nil, ErrProtocol
			})
			_ = server.Close()
			if !errors.Is(err, ErrProtocol) {
				t.Fatalf("Serve error = %v, want ErrProtocol", err)
			}
		})
	}
}

func TestClientCallReturnsRemoteError(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewClient(clientConn, nil)
	defer func() { _ = client.Close() }()
	defer func() { _ = serverConn.Close() }()
	go func() {
		var req Request
		if readFrame(serverConn, &req) == nil {
			_ = writeFrame(serverConn, Response{Version: ProtocolVersion, ID: req.ID, Kind: "response", Error: &RemoteError{Code: "denied", Message: "not allowed"}})
		}
	}()
	err := client.Call(context.Background(), "run", nil, nil)
	remote, ok := errors.AsType[*RemoteError](err)
	if !ok || remote.Code != "denied" || err.Error() != "not allowed" {
		t.Fatalf("Call error = %v, want the operation diagnostic and remote classification", err)
	}
}

func TestServeContextCancellationWaitsForHandlers(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, finished := make(chan struct{}), make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeContext(ctx, serverConn, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			close(started)
			<-ctx.Done()
			close(finished)
			return nil, ctx.Err()
		})
	}()
	if err := writeFrame(clientConn, Request{Version: ProtocolVersion, ID: 1, Method: "wait"}); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	_ = clientConn.Close()
	if err := <-serveDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("ServeContext = %v, want context cancellation", err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("ServeContext returned before handler finished")
	}
}

func framed(payload []byte) []byte {
	data := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(data, uint32(len(payload)))
	copy(data[4:], payload)
	return data
}

func TestServeRejectsNonMonotonicRequestIDs(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(server, func(context.Context, string, json.RawMessage) (any, error) { return struct{}{}, nil })
	}()
	if err := writeFrame(client, Request{Version: ProtocolVersion, ID: 2, Method: "first"}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := readFrame(client, &response); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(client, Request{Version: ProtocolVersion, ID: 1, Method: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := <-serveDone; !errors.Is(err, ErrProtocol) {
		t.Fatalf("Serve error = %v, want ErrProtocol", err)
	}
}

func TestClientCancellationCancelsWorkerHandler(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewClient(clientConn, nil)
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, finished := make(chan struct{}), make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- ServeContext(context.Background(), serverConn, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			close(started)
			<-ctx.Done()
			close(finished)
			return nil, ctx.Err()
		})
	}()
	callDone := make(chan error, 1)
	go func() { callDone <- client.Call(ctx, "cancel-me", nil, nil) }()
	<-started
	cancel()
	if err := <-callDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Call error = %v, want context.Canceled", err)
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker handler did not receive cancellation")
	}
	_ = client.Close()
	if err := <-serveDone; err != nil {
		t.Fatalf("ServeContext error = %v", err)
	}
}

func TestProtocolFailureCancelsAndJoinsActiveOperation(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	started, finished := make(chan struct{}), make(chan struct{})
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(server, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			close(started)
			<-ctx.Done()
			close(finished)
			return nil, ctx.Err()
		})
	}()
	if err := writeFrame(client, Request{Version: ProtocolVersion, ID: 1, Method: "active"}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := writeFrame(client, Request{Version: ProtocolVersion + 1, ID: 2, Method: "invalid"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serveDone:
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("protocol failure = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("invalid protocol left an active operation holding ownership")
	}
	select {
	case <-finished:
	default:
		t.Fatal("server returned before active operation completed")
	}
}

func TestClientRejectsUncorrelatedWorkerFrames(t *testing.T) {
	for _, kind := range []string{"response", "progress"} {
		t.Run(kind, func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			progress := make(chan struct{}, 1)
			client := NewClient(clientConn, func(json.RawMessage) { progress <- struct{}{} })
			defer func() { _ = client.Close() }()
			go func() {
				defer func() { _ = serverConn.Close() }()
				var request Request
				if err := readFrame(serverConn, &request); err != nil {
					return
				}
				_ = writeFrame(serverConn, Response{Version: ProtocolVersion, ID: request.ID + 1, Kind: kind, Event: json.RawMessage(`{}`)})
			}()
			if err := client.Call(context.Background(), "active", nil, nil); !errors.Is(err, ErrProtocol) {
				t.Fatalf("uncorrelated %s frame: %v", kind, err)
			}
			select {
			case <-progress:
				t.Fatal("uncorrelated progress reached the display")
			default:
			}
		})
	}
}

func TestTransportFailureNeverEnablesLocalDispatch(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewClient(clientConn, nil)
	defer func() { _ = client.Close() }()
	selectionMu.RLock()
	previous := selected
	selectionMu.RUnlock()
	Select(client)
	t.Cleanup(func() { Select(previous) })
	_ = serverConn.Close()
	for range 2 {
		handled, err := Call(context.Background(), "must-remain-remote", nil, nil)
		if !handled || err == nil || !RemoteSelected() {
			t.Fatalf("failed worker enabled local dispatch: handled=%v error=%v", handled, err)
		}
	}
}
