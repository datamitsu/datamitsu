// Package provision transports resolved operations to a worker that owns their full mutation lifetime.
package provision

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// ProtocolVersion is the version used by provisioning worker requests and responses.
const ProtocolVersion = 1

const startupNonceSize = 32

// ErrProtocol identifies malformed or incompatible private worker traffic.
var ErrProtocol = errors.New("provision protocol error")

// Request is one provisioning operation, cancellation, or protocol control frame.
type Request struct {
	Version  int             `json:"version"`
	ID       uint64          `json:"id"`
	Kind     string          `json:"kind,omitempty"`
	Method   string          `json:"method,omitempty"`
	Params   json.RawMessage `json:"params,omitempty"`
	CancelID uint64          `json:"cancelId,omitempty"`
}

// Response carries an operation result, error, or progress event.
type Response struct {
	Version int             `json:"version"`
	ID      uint64          `json:"id"`
	Kind    string          `json:"kind"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RemoteError    `json:"error,omitempty"`
	Event   json.RawMessage `json:"event,omitempty"`
}

// RemoteError is a safe error summary returned by the worker.
type RemoteError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error keeps transport classification out of the operation's public diagnostic.
func (e *RemoteError) Error() string { return e.Message }

// ReadStartupHandshake accepts exactly one private 32-byte startup nonce.
func ReadStartupHandshake(r io.Reader) error {
	var nonce [startupNonceSize]byte
	if _, err := io.ReadFull(r, nonce[:]); err != nil {
		return fmt.Errorf("%w: invalid startup handshake: %w", ErrProtocol, err)
	}
	var extra [1]byte
	n, err := r.Read(extra[:])
	if n != 0 || err == nil {
		return fmt.Errorf("%w: startup handshake has trailing data", ErrProtocol)
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: startup handshake did not terminate: %w", ErrProtocol, err)
	}
	return nil
}

func writeFrame(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal provision frame: %w", err)
	}
	size := uint64(len(data))
	if size > math.MaxUint32 {
		return fmt.Errorf("%w: frame too large", ErrProtocol)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(size))
	if err := writeAll(w, header[:]); err != nil {
		return fmt.Errorf("write provision frame header: %w", err)
	}
	if err := writeAll(w, data); err != nil {
		return fmt.Errorf("write provision frame: %w", err)
	}
	return nil
}

func readFrame(r io.Reader, value any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return fmt.Errorf("read provision frame header: %w", err)
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 {
		return fmt.Errorf("%w: empty frame", ErrProtocol)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("read provision frame: %w", err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	return nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return fmt.Errorf("write provision data: %w", err)
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
