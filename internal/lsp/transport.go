package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Run serves until `exit`, stdin EOF, Stop, or a fatal transport error.
//
// A reader goroutine owns stdin and a single worker runs everything that
// touches session state. The reader answers what needs no session on the spot —
// a frame that is not JSON, $/cancelRequest, the document notifications — and
// queues the rest in order, so a cancel is heard while a format runs. Exactly
// one worker keeps the planner, executor, cache and loader single-threaded.
//
// Run never returns while the worker is still running, and it shuts down
// whatever execution cache the session holds by then.
func (s *Server) Run(ctx context.Context) error {
	t := newTransport()
	if !s.attach(t) {
		return nil
	}
	defer s.closeSession()

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.work(ctx, t)
	}()
	go s.read(t)
	<-done
	return t.readErr()
}

// Stop ends Run as if the client had closed stdin: the running request stops at
// its next checkpoint, queued ones are dropped, and Run returns once the worker
// is idle. It is safe from any goroutine, before or during Run.
func (s *Server) Stop() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.stopping = true
	if s.transport != nil {
		s.transport.abort(nil)
	}
}

func (s *Server) attach(t *transport) bool {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.stopping {
		return false
	}
	s.transport = t
	return true
}

// read is the reader goroutine. A frame whose body is read in full but is not
// valid JSON is recoverable: per JSON-RPC 2.0 the server replies Parse Error and
// keeps serving. Every other read error ends the session.
func (s *Server) read(t *transport) {
	for {
		body, err := s.conn.readFrame()
		if t.closed() {
			return // Stop ended the session while this read was blocked
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.abort(nil) // client closed the connection
			} else {
				t.abort(fmt.Errorf("lsp read: %w", err)) // fatal: stream out of sync
			}
			return
		}

		var m message
		if err := json.Unmarshal(body, &m); err != nil {
			// The frame was fully consumed, so the stream stays framed; only this
			// message is bad. id is unknown for invalid JSON, so reply id:null.
			_ = s.conn.replyError(nil, codeParseError, "parse error: "+err.Error())
			continue
		}
		if !s.route(t, &m) {
			return
		}
	}
}

// route answers what the reader handles itself and queues everything else. It
// reports whether to keep reading.
func (s *Server) route(t *transport, m *message) bool {
	switch m.Method {
	case "$/cancelRequest":
		if !m.isRequest() {
			var params cancelParams
			if json.Unmarshal(m.Params, &params) == nil && len(params.ID) > 0 {
				t.registry.cancel(requestKey(params.ID))
			}
			return true
		}
	case "textDocument/didOpen":
		// Owned by the reader so full-sync changes replace each other instead of
		// piling up behind a long format.
		s.onDidOpen(m)
		return true
	case "textDocument/didChange":
		s.onDidChange(m)
		return true
	case "textDocument/didClose":
		s.onDidClose(m)
		return true
	case "shutdown":
		if m.isRequest() {
			// The client is leaving: its shutdown should not wait behind a chain of
			// fix tools. A format stops at its next checkpoint, and one still queued
			// is answered without running. Other requests do no work worth stopping,
			// and a queued initialize must still initialize.
			t.registry.stopFormatting()
			t.shutdownRead = true
		}
	case "exit":
		if !t.shutdownRead {
			t.registry.stopFormatting()
		}
		t.queue.push(job{msg: m})
		t.queue.close()
		return false
	}

	j := job{msg: m}
	formatting := m.Method == "textDocument/formatting"
	if m.isRequest() {
		j.req = t.registry.add(m.ID, formatting)
	}
	if formatting {
		j.snap = s.snapshot(m)
	}
	t.queue.push(j)
	return true
}

// snapshot is the text of the document a formatting request names, as of the
// request: later changes apply to the next format.
func (s *Server) snapshot(m *message) *docSnapshot {
	var params formattingParams
	if json.Unmarshal(m.Params, &params) != nil {
		return &docSnapshot{}
	}
	text, open := s.docs[params.TextDocument.URI]
	return &docSnapshot{text: text, open: open}
}

// work is the worker goroutine: it runs queued messages one at a time until the
// queue closes or `exit` has been handled.
func (s *Server) work(ctx context.Context, t *transport) {
	for {
		j, ok := t.queue.pop()
		if !ok {
			return
		}
		if j.req != nil && j.req.stopped() {
			_ = s.conn.replyError(j.req.id, codeRequestCancelled, "request cancelled")
			t.registry.finish(j.req)
			continue
		}
		s.active = j.req
		exit := s.dispatch(ctx, j.msg, j.snap)
		s.active = nil
		if j.req != nil {
			t.registry.finish(j.req)
		}
		if exit {
			return
		}
	}
}

// stopRequested reports whether the request the worker is running has been
// cancelled, or the session is ending. Always false outside Run.
func (s *Server) stopRequested() bool {
	return s.active != nil && s.active.stopped()
}

// settle marks the running request as past its last checkpoint, so it is
// answered with what it did and a later cancel finds nothing to stop. It
// reports false when the request was stopped first.
func (s *Server) settle() bool {
	return s.active == nil || s.active.settle()
}

// docSnapshot is a document's text as of a formatting request; open is false
// when the client never opened it.
type docSnapshot struct {
	text []byte
	open bool
}

// job is one queued message; req is nil for a notification.
type job struct {
	msg  *message
	req  *request
	snap *docSnapshot
}

// transport is the state the reader and the worker share for one Run.
type transport struct {
	registry *registry
	queue    *jobQueue

	// shutdownRead is the reader's own: whether it has read shutdown.
	shutdownRead bool

	mu  sync.Mutex
	err error
}

func newTransport() *transport {
	return &transport{registry: &registry{reqs: map[string]*request{}}, queue: newJobQueue()}
}

// abort ends the session: the running request stops at its next checkpoint and
// every queued message is dropped without a reply. err is the first fatal read
// error, if any.
func (t *transport) abort(err error) {
	t.mu.Lock()
	if t.err == nil {
		t.err = err
	}
	t.mu.Unlock()

	t.registry.stopAll()
	for _, j := range t.queue.closeAndDrain() {
		if j.req != nil {
			t.registry.finish(j.req)
		}
	}
}

func (t *transport) closed() bool { return t.queue.isClosed() }

func (t *transport) readErr() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.err
}

// request is a client request between being read and being answered. A
// cancel, shutdown or the end of the session stops it unless it has settled.
type request struct {
	id         json.RawMessage
	key        string
	formatting bool
	state      atomic.Int32
}

const (
	requestLive int32 = iota
	requestStopped
	requestSettled
)

func (r *request) stop() { r.state.CompareAndSwap(requestLive, requestStopped) }

func (r *request) stopped() bool { return r.state.Load() == requestStopped }

func (r *request) settle() bool {
	return r.state.CompareAndSwap(requestLive, requestSettled) || r.state.Load() == requestSettled
}

// registry tracks requests by id. A request leaves it when the worker has
// answered it, so a cancel that arrives later finds nothing and is ignored.
type registry struct {
	mu   sync.Mutex
	reqs map[string]*request
}

func (r *registry) add(id json.RawMessage, formatting bool) *request {
	req := &request{id: id, key: requestKey(id), formatting: formatting}
	r.mu.Lock()
	r.reqs[req.key] = req
	r.mu.Unlock()
	return req
}

// cancel stops the request with this key; an unknown or answered id is ignored.
func (r *registry) cancel(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if req, ok := r.reqs[key]; ok {
		req.stop()
	}
}

func (r *registry) stopAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, req := range r.reqs {
		req.stop()
	}
}

func (r *registry) stopFormatting() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, req := range r.reqs {
		if req.formatting {
			req.stop()
		}
	}
}

func (r *registry) finish(req *request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reqs[req.key] == req {
		delete(r.reqs, req.key)
	}
}

// requestKey spells a request id one way: decoded and encoded again, so the
// same id always matches whatever whitespace or escapes it arrived with, while
// 1 and "1" stay different ids.
func requestKey(id json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(id))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return string(id)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(id)
	}
	return string(b)
}

// jobQueue is unbounded: a reader blocked on a full queue could not see the
// next cancel.
type jobQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	jobs   []job
	closed bool
}

func newJobQueue() *jobQueue {
	q := &jobQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push queues j; a closed queue drops it.
func (q *jobQueue) push(j job) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.jobs = append(q.jobs, j)
	q.cond.Signal()
}

// pop waits for the next job, and reports false once the queue is closed and
// empty.
func (q *jobQueue) pop() (job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.jobs) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.jobs) == 0 {
		return job{}, false
	}
	j := q.jobs[0]
	q.jobs[0] = job{}
	q.jobs = q.jobs[1:]
	return j, true
}

// close lets the worker finish what is queued and then stop.
func (q *jobQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}

func (q *jobQueue) closeAndDrain() []job {
	q.mu.Lock()
	defer q.mu.Unlock()
	dropped := q.jobs
	q.jobs = nil
	q.closed = true
	q.cond.Broadcast()
	return dropped
}

func (q *jobQueue) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}
