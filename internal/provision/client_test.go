package provision

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
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

func TestClientReceivesCorrelatedProgress(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewClient(clientConn, nil)
	defer func() { _ = client.Close() }()
	defer func() { _ = serverConn.Close() }()
	progress := make(chan json.RawMessage, 1)
	client.SetProgress(func(event json.RawMessage) { progress <- event })
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(serverConn, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			if err := Progress(ctx, map[string]string{"phase": "verify"}); err != nil {
				return nil, err
			}
			return map[string]bool{"ok": true}, nil
		})
	}()
	var result struct {
		OK bool `json:"ok"`
	}
	if err := client.Call(context.Background(), "progress-test", nil, &result); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if !result.OK {
		t.Fatalf("response = %#v", result)
	}
	select {
	case event := <-progress:
		if string(event) != `{"phase":"verify"}` {
			t.Fatalf("progress event = %s", event)
		}
	case <-time.After(time.Second):
		t.Fatal("worker progress did not reach the client")
	}
	_ = client.Close()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestClientRejectsMalformedResponseVersion(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewClient(clientConn, nil)
	defer func() { _ = client.Close() }()
	go func() {
		defer func() { _ = serverConn.Close() }()
		var request Request
		if readFrame(serverConn, &request) == nil {
			_ = writeFrame(serverConn, Response{Version: ProtocolVersion + 1, ID: request.ID, Kind: "response"})
		}
	}()
	if err := client.Call(context.Background(), "version-test", nil, nil); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Call() error = %v, want ErrProtocol", err)
	}
}

func TestClientRejectsUnmarshalablePayload(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewClient(clientConn, nil)
	defer func() { _ = client.Close() }()
	defer func() { _ = serverConn.Close() }()
	if err := client.Call(context.Background(), "bad-payload", make(chan int), nil); err == nil || !strings.Contains(err.Error(), "marshal provision request") {
		t.Fatalf("Call() error = %v, want payload marshal error", err)
	}
}

func TestServeReturnsProgressEncodingFailure(t *testing.T) {
	client, server := net.Pipe()
	defer func() { _ = client.Close() }()
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- Serve(server, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
			return nil, Progress(ctx, make(chan int))
		})
	}()
	if err := writeFrame(client, Request{Version: ProtocolVersion, ID: 1, Kind: "request", Method: "progress-error"}); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := readFrame(client, &response); err != nil {
		t.Fatalf("read error response: %v", err)
	}
	if response.Error == nil || !strings.Contains(response.Error.Message, "encode provisioning progress") {
		t.Fatalf("response error = %#v, want progress encoding failure", response.Error)
	}
	_ = client.Close()
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}

func TestReadStartupHandshakeRequiresExactNonceAndEOF(t *testing.T) {
	nonce := make([]byte, startupNonceSize)
	if err := ReadStartupHandshake(strings.NewReader(string(nonce))); err != nil {
		t.Fatalf("valid startup handshake = %v", err)
	}
	for _, input := range [][]byte{nil, nonce[:len(nonce)-1], append(append([]byte(nil), nonce...), 1)} {
		if err := ReadStartupHandshake(strings.NewReader(string(input))); !errors.Is(err, ErrProtocol) {
			t.Errorf("ReadStartupHandshake(%d bytes) = %v, want ErrProtocol", len(input), err)
		}
	}
	boom := errors.New("pipe read failed")
	reader := io.MultiReader(strings.NewReader(string(nonce)), errorReader{err: boom})
	if err := ReadStartupHandshake(reader); !errors.Is(err, ErrProtocol) || !errors.Is(err, boom) {
		t.Fatalf("non-EOF handshake termination = %v, want protocol and underlying errors", err)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestWriteFrameRejectsUnmarshalableValues(t *testing.T) {
	if err := writeFrame(io.Discard, make(chan int)); err == nil || !strings.Contains(err.Error(), "marshal provision frame") {
		t.Fatalf("writeFrame() error = %v, want JSON marshal failure", err)
	}
}

func TestServeRejectsNilHandlerAndInvalidRequestKinds(t *testing.T) {
	client, server := net.Pipe()
	if err := Serve(server, nil); err == nil || !strings.Contains(err.Error(), "handler is required") {
		t.Fatalf("Serve(nil handler) = %v", err)
	}
	_ = server.Close()
	_ = client.Close()
	for _, req := range []Request{
		{Version: ProtocolVersion, ID: 1, Kind: "unexpected", Method: "run"},
		{Version: ProtocolVersion, ID: 1, Kind: "cancel", CancelID: 1, Method: "extra"},
		{Version: ProtocolVersion, ID: 1, Kind: "cancel", CancelID: 0},
	} {
		client, server := net.Pipe()
		go func() { _ = writeFrame(client, req); _ = client.Close() }()
		if err := Serve(server, func(context.Context, string, json.RawMessage) (any, error) {
			t.Error("handler invoked for invalid request")
			return nil, errors.New("invalid request reached handler")
		}); !errors.Is(err, ErrProtocol) {
			t.Errorf("Serve(%#v) = %v, want ErrProtocol", req, err)
		}
		_ = server.Close()
	}
}

func TestProgressWithoutWorkerContextIsNoop(t *testing.T) {
	if HasProgress(context.Background()) {
		t.Fatal("background context unexpectedly reports progress support")
	}
	if err := Progress(context.Background(), map[string]string{"phase": "install"}); err != nil {
		t.Fatalf("Progress() without worker context = %v", err)
	}
}

func TestClientRejectsMalformedTypedResponse(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	client := NewClient(clientConn, nil)
	defer func() { _ = client.Close() }()
	go func() {
		defer func() { _ = serverConn.Close() }()
		var req Request
		if readFrame(serverConn, &req) == nil {
			_ = writeFrame(serverConn, Response{Version: ProtocolVersion, ID: req.ID, Kind: "response", Result: json.RawMessage(`[]`)})
		}
	}()
	var result struct {
		Value string `json:"value"`
	}
	if err := client.Call(context.Background(), "malformed-result", nil, &result); err == nil || !strings.Contains(err.Error(), "decode provision response") {
		t.Fatalf("Call() error = %v, want typed response decode failure", err)
	}
}
