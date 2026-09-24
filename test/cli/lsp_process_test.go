package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

// lspProcess is a running `datamitsu lsp` with a live stdin, so a session can
// wait for a response before it sends the next message, as an editor does.
// Pipelining a format ahead of shutdown would test shutdown instead: it answers
// a format that has not started yet with RequestCancelled.
type lspProcess struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	frames chan map[string]json.RawMessage
	// early holds responses read while waiting for another id.
	early map[string]map[string]json.RawMessage

	mu     sync.Mutex
	stdout bytes.Buffer // every byte of stdout, for the frames-only contract check
	stderr bytes.Buffer
}

// startLsp starts `datamitsu lsp args...` in dir with the harness's hermetic
// environment plus env.
func startLsp(t *testing.T, dir string, env []string, args ...string) *lspProcess {
	t.Helper()
	bin := clitest.BuildOnce(t)
	ctx, cancel := context.WithTimeout(context.Background(), clitest.DefaultTimeout)
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, bin, append([]string{"lsp"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(clitest.BaseEnv(t.TempDir()), env...)
	p := &lspProcess{t: t, cmd: cmd, frames: make(chan map[string]json.RawMessage, 16), early: map[string]map[string]json.RawMessage{}}
	cmd.Stderr = lockedWriter{&p.mu, &p.stderr}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p.stdin = stdin
	if err := cmd.Start(); err != nil {
		t.Fatalf("start lsp: %v", err)
	}
	t.Cleanup(func() {
		_ = p.stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})

	go func() {
		defer close(p.frames)
		r := bufio.NewReader(io.TeeReader(stdout, lockedWriter{&p.mu, &p.stdout}))
		for {
			body, err := readLSPFrame(r)
			if err != nil {
				return // EOF, or bytes that are not a frame: finish reports them
			}
			var m map[string]json.RawMessage
			if json.Unmarshal(body, &m) != nil {
				return
			}
			p.frames <- m
		}
	}()
	return p
}

func (p *lspProcess) send(body string) {
	p.t.Helper()
	if _, err := io.WriteString(p.stdin, frame(body)); err != nil {
		p.t.Fatalf("send %s: %v", body, err)
	}
}

// await returns the response to the request with this id (as JSON, e.g. `2`).
func (p *lspProcess) await(id string) map[string]json.RawMessage {
	p.t.Helper()
	if m, ok := p.early[id]; ok {
		delete(p.early, id)
		return m
	}
	timeout := time.After(clitest.DefaultTimeout)
	for {
		select {
		case m, ok := <-p.frames:
			if !ok {
				p.t.Fatalf("lsp closed stdout before answering id %s\n--- stderr ---\n%s", id, p.stderrText())
			}
			got := strings.TrimSpace(string(m["id"]))
			if got == id {
				return m
			}
			p.early[got] = m
		case <-timeout:
			p.t.Fatalf("no response to id %s", id)
		}
	}
}

// finish shuts the server down the way a client does, then checks the stdout
// contract: nothing but frames, ever.
func (p *lspProcess) finish() clitest.Result {
	p.t.Helper()
	p.send(`{"jsonrpc":"2.0","id":"shutdown","method":"shutdown"}`)
	if got := strings.TrimSpace(string(p.await(`"shutdown"`)["result"])); got != "null" {
		p.t.Errorf("shutdown result = %s, want null", got)
	}
	p.send(`{"jsonrpc":"2.0","method":"exit"}`)
	_ = p.stdin.Close()
	for m := range p.frames { // drained until the reader sees EOF
		p.early[strings.TrimSpace(string(m["id"]))] = m
	}
	err := p.cmd.Wait()

	p.mu.Lock()
	stdout := p.stdout.String()
	p.mu.Unlock()
	parseAllFrames(p.t, []byte(stdout))
	return clitest.Result{Stdout: stdout, Stderr: p.stderrText(), ExitCode: clitest.ExitCodeOf(err), Err: err}
}

func (p *lspProcess) stderrText() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stderr.String()
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func readLSPFrame(r *bufio.Reader) ([]byte, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		v, ok := strings.CutPrefix(line, "Content-Length:")
		if !ok {
			continue
		}
		if n, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
			return nil, fmt.Errorf("bad Content-Length %q: %w", v, err)
		}
	}
	if n < 0 {
		return nil, errors.New("frame without Content-Length")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
