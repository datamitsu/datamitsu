package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/binmanager"
	"github.com/datamitsu/datamitsu/internal/config"
	"github.com/datamitsu/datamitsu/internal/uievent"
)

const sessionTimeout = 30 * time.Second

// pipeSession runs a server's Run over pipes, so a test decides when each
// message arrives.
type pipeSession struct {
	t      *testing.T
	in     *io.PipeWriter
	frames chan map[string]json.RawMessage
	early  map[string]map[string]json.RawMessage
	done   chan error
}

func runSession(t *testing.T, s *Server) *pipeSession {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s.conn = newConn(inR, outW)
	p := &pipeSession{
		t:      t,
		in:     inW,
		frames: make(chan map[string]json.RawMessage, 64),
		early:  map[string]map[string]json.RawMessage{},
		done:   make(chan error, 1),
	}
	go func() {
		err := s.Run(context.Background())
		_ = outW.Close()
		p.done <- err
	}()
	go func() {
		defer close(p.frames)
		r := bufio.NewReader(outR)
		for {
			m, err := readTestFrame(r)
			if err != nil {
				return
			}
			p.frames <- m
		}
	}()
	t.Cleanup(func() { _ = inW.Close() })
	return p
}

func readTestFrame(r *bufio.Reader) (map[string]json.RawMessage, error) {
	c := &conn{r: r}
	body, err := c.readFrame()
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("decode %q: %w", body, err)
	}
	return m, nil
}

// send writes one message. The pipe hands it over only when the server reads
// it.
func (p *pipeSession) send(body string) {
	p.t.Helper()
	if _, err := io.WriteString(p.in, frameStr(body)); err != nil {
		p.t.Fatalf("send %s: %v", body, err)
	}
}

// sendHandled writes one message and returns once the reader has acted on it.
// Each frame is small enough to arrive in one read, so the reader asks the pipe
// for the next one only after it has routed this one — which is when the
// second write, of a notification the server ignores, can return.
func (p *pipeSession) sendHandled(body string) {
	p.t.Helper()
	p.send(body)
	p.send(`{"jsonrpc":"2.0","method":"$/test/sync"}`)
}

// await returns the response to the request whose id is id (as JSON).
func (p *pipeSession) await(id string) map[string]json.RawMessage {
	p.t.Helper()
	if m, ok := p.early[id]; ok {
		delete(p.early, id)
		return m
	}
	timeout := time.After(sessionTimeout)
	for {
		select {
		case m, ok := <-p.frames:
			if !ok {
				p.t.Fatalf("the server closed its output before answering id %s", id)
			}
			got := string(m["id"])
			if got == id {
				return m
			}
			p.early[got] = m
		case <-timeout:
			p.t.Fatalf("no response to id %s", id)
		}
	}
}

// end waits for Run to return, and returns every response not yet awaited.
func (p *pipeSession) end() (map[string]map[string]json.RawMessage, error) {
	p.t.Helper()
	var err error
	select {
	case err = <-p.done:
	case <-time.After(sessionTimeout):
		p.t.Fatal("Run did not return")
	}
	for m := range p.frames {
		p.early[string(m["id"])] = m
	}
	return p.early, err
}

// errorCode is a response's error code, 0 for a result.
func errorCode(t *testing.T, m map[string]json.RawMessage) int {
	t.Helper()
	if _, ok := m["error"]; !ok {
		return 0
	}
	var e responseError
	if err := json.Unmarshal(m["error"], &e); err != nil {
		t.Fatalf("decode error %s: %v", m["error"], err)
	}
	return e.Code
}

// gate is a FIFO a fake tool blocks on: opening it for writing waits until the
// tool is running, and closing it lets the tool finish.
type gate struct {
	t    *testing.T
	path string
	w    *os.File
}

func newGate(t *testing.T) *gate {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gate")
	if out, err := exec.Command("mkfifo", path).CombinedOutput(); err != nil {
		t.Skipf("mkfifo unavailable: %v: %s", err, out)
	}
	return &gate{t: t, path: path}
}

// awaitTool returns once the gated tool is running.
func (g *gate) awaitTool() {
	g.t.Helper()
	opened := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(g.path, os.O_WRONLY, 0)
		if err != nil {
			opened <- nil
			return
		}
		opened <- f
	}()
	select {
	case f := <-opened:
		if f == nil {
			g.t.Fatal("open the gate")
		}
		g.w = f
	case <-time.After(sessionTimeout):
		g.t.Fatal("the gated tool never started")
	}
}

func (g *gate) release() {
	g.t.Helper()
	if err := g.w.Close(); err != nil {
		g.t.Fatal(err)
	}
}

// gatedConfig has three fix groups on *.txt appending 1, 2 and 3. The first
// blocks on the gate while it formats gate.txt, so a test can act while a
// format is inside its first group.
func gatedConfig(g *gate) *config.Config {
	return &config.Config{
		Apps: binmanager.MapOfApps{
			"append-1": shellApp(`printf 1 >> "$1"; case "$1" in */gate.txt) cat '` + g.path + `' > /dev/null;; esac`),
			"append-2": shellApp(`printf 2 >> "$1"`),
			"append-3": shellApp(`printf 3 >> "$1"`),
		},
		Tools: config.MapOfTools{
			"first":  {Name: "first", Operations: fixOp("append-1", 0)},
			"second": {Name: "second", Operations: fixOp("append-2", 1)},
			"third":  {Name: "third", Operations: fixOp("append-3", 2)},
		},
	}
}

// gatedSession is an initialized session over gatedConfig with gate.txt, a.txt
// and b.txt holding "x".
func gatedSession(t *testing.T) (p *pipeSession, s *Server, g *gate, files map[string]string) {
	t.Helper()
	return gatedSessionWith(t, gatedConfig)
}

func gatedSessionWith(t *testing.T, build func(*gate) *config.Config) (p *pipeSession, s *Server, g *gate, files map[string]string) {
	t.Helper()
	t.Setenv("DATAMITSU_CACHE_DIR", t.TempDir())
	g = newGate(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files = map[string]string{}
	for _, name := range []string{"gate.txt", "a.txt", "b.txt"} {
		files[name] = filepath.Join(root, name)
		if err := os.WriteFile(files[name], []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s = newConfiguredServer(t, build(g), root)
	p = runSession(t, s)
	p.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{}}`)
	p.await("0")
	return p, s, g, files
}

func formatRequest(id, path string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"textDocument/formatting","params":{"textDocument":{"uri":%q}}}`,
		id, "file://"+path)
}

func cancelRequest(id string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":%s}}`, id)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A format cancelled while it runs stops before its next group of tools: the
// group that was running finishes and reports its end, nothing later starts,
// and the request is answered RequestCancelled. One cancelled while queued never
// runs at all.
func TestCancelRequest(t *testing.T) {
	sink := captureEvents(t)
	p, _, g, files := gatedSession(t)

	p.send(formatRequest("1", files["gate.txt"]))
	g.awaitTool()
	p.send(formatRequest("2", files["a.txt"]))
	p.sendHandled(cancelRequest("1"))
	p.sendHandled(cancelRequest("2"))
	g.release()

	if code := errorCode(t, p.await("1")); code != codeRequestCancelled {
		t.Errorf("running format answered %d, want %d", code, codeRequestCancelled)
	}
	if code := errorCode(t, p.await("2")); code != codeRequestCancelled {
		t.Errorf("queued format answered %d, want %d", code, codeRequestCancelled)
	}
	if got := readFile(t, files["gate.txt"]); got != "x1" {
		t.Errorf("gate.txt = %q, want only the first group applied", got)
	}
	if got := readFile(t, files["a.txt"]); got != "x" {
		t.Errorf("a.txt = %q: a format cancelled while queued ran", got)
	}

	phases := sink.ofType(uievent.TypePhase)
	if len(phases) != 1 {
		t.Fatalf("phase events = %+v, want only the running format's", phases)
	}
	var started, ended []string
	for _, e := range sink.ofType(uievent.TypeToolRun) {
		if e.Status == uievent.StatusStart {
			started = append(started, e.Tool)
		} else {
			ended = append(ended, e.Tool)
		}
	}
	if strings.Join(started, ",") != "first" || strings.Join(ended, ",") != "first" {
		t.Errorf("tool_run starts %v, ends %v; want the first group's tool started and ended", started, ended)
	}
	var notice string
	for _, e := range sink.ofType(uievent.TypeLog) {
		if strings.Contains(e.Msg, "cancelled") {
			notice = e.Msg
		}
	}
	if want := "format gate.txt: cancelled after 1 of 3 tool groups; did not run: second, third"; notice != want {
		t.Errorf("cancel notice = %q, want %q", notice, want)
	}
	done := sink.ofType(uievent.TypeDone)
	if len(done) != 1 || done[0].Status != uievent.StatusFail || done[0].Msg != "cancelled" ||
		done[0].Success == nil || *done[0].Success || done[0].Runs != 1 || done[0].Skipped != 2 {
		t.Errorf("done = %+v, want a failed format with msg cancelled, 1 run and 2 skipped", done)
	}
}

// A cancel that arrives while the last group runs finds no checkpoint left, yet
// the request is answered as cancelled and the stream agrees: a one-group plan
// is the common case, where every cancel of a running format lands there.
func TestCancelDuringTheLastGroup(t *testing.T) {
	sink := captureEvents(t)
	p, _, g, files := gatedSessionWith(t, func(g *gate) *config.Config {
		return &config.Config{
			Apps:  binmanager.MapOfApps{"append-1": shellApp(`printf 1 >> "$1"; cat '` + g.path + `' > /dev/null`)},
			Tools: config.MapOfTools{"only": {Name: "only", Operations: fixOp("append-1", 0)}},
		}
	})

	p.send(formatRequest("1", files["gate.txt"]))
	g.awaitTool()
	p.sendHandled(cancelRequest("1"))
	g.release()

	if code := errorCode(t, p.await("1")); code != codeRequestCancelled {
		t.Errorf("format answered %d, want %d", code, codeRequestCancelled)
	}
	if got := readFile(t, files["gate.txt"]); got != "x1" {
		t.Errorf("gate.txt = %q, want the group that ran applied", got)
	}
	if n := countContaining(logsAt(sink, uievent.LevelInfo), "format gate.txt: cancelled after 1 of 1 tool groups; every group ran"); n != 1 {
		t.Errorf("%d cancel notices, want 1: %q", n, logsAt(sink, uievent.LevelInfo))
	}
	done := sink.ofType(uievent.TypeDone)
	if len(done) != 1 || done[0].Status != uievent.StatusFail || done[0].Msg != "cancelled" ||
		done[0].Success == nil || *done[0].Success || done[0].Runs != 1 || done[0].Skipped != 0 {
		t.Errorf("done = %+v, want a failed format with msg cancelled and 1 run", done)
	}
}

// A cancel names a request by id: an unknown or already answered id changes
// nothing, and 1 and "1" are different requests.
func TestCancelRequestMatchesIDsExactly(t *testing.T) {
	captureEvents(t)
	p, _, g, files := gatedSession(t)

	p.sendHandled(cancelRequest("99"))
	p.send(formatRequest("5", files["b.txt"]))
	if code := errorCode(t, p.await("5")); code != 0 {
		t.Fatalf("format answered %d", code)
	}
	p.sendHandled(cancelRequest("5")) // already answered

	p.send(formatRequest("7", files["gate.txt"]))
	g.awaitTool()
	p.send(formatRequest("1", files["a.txt"]))
	p.send(formatRequest(`"1"`, files["b.txt"]))
	p.sendHandled(cancelRequest(`"1"`))
	g.release()

	for id, want := range map[string]int{"7": 0, "1": 0, `"1"`: codeRequestCancelled} {
		if code := errorCode(t, p.await(id)); code != want {
			t.Errorf("id %s answered %d, want %d", id, code, want)
		}
	}
	for name, want := range map[string]string{"gate.txt": "x123", "a.txt": "x123", "b.txt": "x123"} {
		if got := readFile(t, files[name]); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	p.send(`{"jsonrpc":"2.0","id":"s","method":"shutdown"}`)
	p.await(`"s"`)
	p.send(`{"jsonrpc":"2.0","method":"exit"}`)
	rest, err := p.end()
	if err != nil || len(rest) != 0 {
		t.Errorf("Run = %v with unanswered or duplicate responses %v", err, rest)
	}
}

// shutdown stops a running format at its next checkpoint and answers a queued
// one without running it; it is answered once the worker is idle, and requests
// after it are still rejected.
func TestShutdownWhileAFormatRuns(t *testing.T) {
	captureEvents(t)
	p, s, g, files := gatedSession(t)

	p.send(formatRequest("1", files["gate.txt"]))
	g.awaitTool()
	p.send(formatRequest("2", files["a.txt"]))
	p.sendHandled(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`)
	p.send(formatRequest("4", files["b.txt"]))
	g.release()

	for id, want := range map[string]int{"1": codeRequestCancelled, "2": codeRequestCancelled, "3": 0, "4": codeInvalidRequest} {
		if code := errorCode(t, p.await(id)); code != want {
			t.Errorf("id %s answered %d, want %d", id, code, want)
		}
	}
	for name, want := range map[string]string{"gate.txt": "x1", "a.txt": "x", "b.txt": "x"} {
		if got := readFile(t, files[name]); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	p.send(`{"jsonrpc":"2.0","method":"exit"}`)
	if _, err := p.end(); err != nil {
		t.Errorf("Run: %v", err)
	}
	if s.ExitCode() != 0 {
		t.Errorf("exit code = %d, want 0", s.ExitCode())
	}
}

// When the client goes away — stdin closes, or a signal stops the server — the
// running format stops at its next checkpoint, queued requests are dropped
// unanswered, and Run returns only once the worker is done.
func TestSessionEndsWhileAFormatRuns(t *testing.T) {
	tests := []struct {
		name string
		end  func(p *pipeSession, s *Server)
	}{
		{name: "stdin EOF", end: func(p *pipeSession, s *Server) {
			_ = p.in.Close()
			waitForAbort(p.t, s)
		}},
		{name: "Stop", end: func(_ *pipeSession, s *Server) { s.Stop() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captureEvents(t)
			p, s, g, files := gatedSession(t)

			p.send(formatRequest("1", files["gate.txt"]))
			g.awaitTool()
			p.sendHandled(formatRequest("2", files["a.txt"]))
			tt.end(p, s)
			g.release()

			rest, err := p.end()
			if err != nil {
				t.Errorf("Run: %v", err)
			}
			if _, answered := rest["2"]; answered {
				t.Error("a request queued when the session ended was answered")
			}
			if got := readFile(t, files["gate.txt"]); got != "x1" {
				t.Errorf("gate.txt = %q, want only the first group applied", got)
			}
			if got := readFile(t, files["a.txt"]); got != "x" {
				t.Errorf("a.txt = %q, want untouched", got)
			}
		})
	}
}

// waitForAbort returns once the reader has seen stdin close. EOF gives the test
// no message to wait on, so it watches the queue close instead.
func waitForAbort(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(sessionTimeout)
	for time.Now().Before(deadline) {
		s.runMu.Lock()
		tr := s.transport
		s.runMu.Unlock()
		if tr != nil && tr.closed() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the reader never saw stdin close")
}

func TestRequestKey(t *testing.T) {
	tests := []struct {
		a, b string
		same bool
	}{
		{a: `1`, b: `1`, same: true},
		{a: `1`, b: `"1"`, same: false},
		{a: `"a"`, b: ` "a" `, same: true},
		{a: `"a"`, b: `"\u0061"`, same: true},
		{a: `12345678901234567890`, b: `12345678901234567890`, same: true},
	}
	for _, tt := range tests {
		if got := requestKey(json.RawMessage(tt.a)) == requestKey(json.RawMessage(tt.b)); got != tt.same {
			t.Errorf("requestKey(%s) == requestKey(%s) is %v, want %v", tt.a, tt.b, got, tt.same)
		}
	}
}
