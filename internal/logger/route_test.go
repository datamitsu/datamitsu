package logger

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/uievent"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type capture struct {
	mu     sync.Mutex
	events []uievent.Event
}

func (c *capture) emit(e uievent.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *capture) all() []uievent.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uievent.Event(nil), c.events...)
}

// routeTo installs a route for one test. The route and the level are
// process-wide, so these tests must not run in parallel.
func routeTo(t *testing.T, level zapcore.Level) *capture {
	t.Helper()
	previous := Level()
	SetLevel(level)
	c := &capture{}
	Route(c.emit)
	t.Cleanup(func() {
		Route(nil)
		SetLevel(previous)
	})
	return c
}

// Several packages derive their logger into a package variable at init, long
// before a command decides stderr is a JSON-L stream. Those must follow the
// route too, or their lines stay plain text inside the stream.
func TestRouteReachesLoggersDerivedBeforeIt(t *testing.T) {
	withFields := Logger.With(zap.String("component", "cache"))
	namespaced := Logger.With(zap.Namespace("binmanager"))
	named := Logger.Named("config")

	got := routeTo(t, zapcore.DebugLevel)

	withFields.Warn("failed to load cache", zap.Error(errors.New("corrupt")))
	namespaced.Debug("downloading", zap.String("name", "shfmt"))
	named.Info("version check skipped")

	want := []uievent.Event{
		{Type: uievent.TypeLog, Level: uievent.LevelWarn, Msg: `failed to load cache {"component":"cache","error":"corrupt"}`},
		{Type: uievent.TypeLog, Level: uievent.LevelDebug, Msg: `downloading {"binmanager":{"name":"shfmt"}}`},
		{Type: uievent.TypeLog, Level: uievent.LevelInfo, Msg: `version check skipped`},
	}
	events := got.all()
	if len(events) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, e := range events {
		if !strings.HasPrefix(e.OpID, "log-") {
			t.Errorf("event %d op_id = %q, want a log-* id", i, e.OpID)
		}
		e.OpID = ""
		if e != want[i] {
			t.Errorf("event %d = %+v, want %+v", i, e, want[i])
		}
	}
	if events[0].OpID == events[1].OpID {
		t.Error("two entries share an op_id")
	}
}

// Only what the active level lets through is routed: the route replaces where
// a line goes, not which lines exist.
func TestRouteHonoursTheActiveLevel(t *testing.T) {
	got := routeTo(t, zapcore.WarnLevel)

	Logger.Debug("hidden")
	Logger.Info("hidden")
	Logger.Warn("shown")

	events := got.all()
	if len(events) != 1 || events[0].Msg != "shown" {
		t.Errorf("events = %+v, want only the warning", events)
	}
}

func TestEventLevel(t *testing.T) {
	tests := []struct {
		level zapcore.Level
		want  string
	}{
		{zapcore.DebugLevel - 1, uievent.LevelDebug},
		{zapcore.DebugLevel, uievent.LevelDebug},
		{zapcore.InfoLevel, uievent.LevelInfo},
		{zapcore.WarnLevel, uievent.LevelWarn},
		{zapcore.ErrorLevel, uievent.LevelError},
		{zapcore.DPanicLevel, uievent.LevelError},
		{zapcore.PanicLevel, uievent.LevelError},
		{zapcore.FatalLevel, uievent.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.level.String(), func(t *testing.T) {
			if got := eventLevel(tt.level); got != tt.want {
				t.Errorf("eventLevel(%v) = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

// The message carries the fields in the order they were added, With's first,
// as one compact JSON object; the same entry always renders the same way.
func TestRoutedMessage(t *testing.T) {
	tests := []struct {
		name   string
		with   []zapcore.Field
		fields []zapcore.Field
		want   string
	}{
		{name: "no fields", want: "msg"},
		{
			name:   "call fields",
			fields: []zapcore.Field{zap.String("file", "a.go"), zap.Int("n", 2), zap.Bool("ok", true)},
			want:   `msg {"file":"a.go","n":2,"ok":true}`,
		},
		{
			name:   "With fields come first",
			with:   []zapcore.Field{zap.String("component", "cache")},
			fields: []zapcore.Field{zap.String("file", "a.go")},
			want:   `msg {"component":"cache","file":"a.go"}`,
		},
		{
			name: "a namespace with no field in it is left out",
			with: []zapcore.Field{zap.Namespace("cmd")},
			want: "msg",
		},
		{
			name:   "a namespace nests the fields after it",
			with:   []zapcore.Field{zap.String("top", "x"), zap.Namespace("cmd")},
			fields: []zapcore.Field{zap.String("tool", "eslint")},
			want:   `msg {"top":"x","cmd":{"tool":"eslint"}}`,
		},
		{
			name:   "skipped fields add nothing",
			fields: []zapcore.Field{zap.Skip(), zap.Error(nil)},
			want:   "msg",
		},
		{
			name:   "errors and durations read as text",
			fields: []zapcore.Field{zap.Error(errors.New(`bad "quote"`)), zap.Duration("took", 1500*time.Millisecond)},
			want:   `msg {"error":"bad \"quote\"","took":"1.5s"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := routedMessage("msg", tt.with, tt.fields)
			if got != tt.want {
				t.Errorf("routedMessage() =\n  %s\nwant\n  %s", got, tt.want)
			}
			if again := routedMessage("msg", tt.with, tt.fields); again != got {
				t.Errorf("second render %q differs from the first %q", again, got)
			}
		})
	}
}

// Without a route the console line is byte-for-byte what the plain console core
// writes, and removing a route brings it back.
func TestConsoleOutputWithoutARoute(t *testing.T) {
	logAll := func(l *zap.Logger) {
		l.Warn("plain")
		l.With(zap.String("component", "cache")).Warn("with", zap.Int("n", 1))
		l.With(zap.Namespace("cmd")).Error("namespaced", zap.String("tool", "tsc"))
	}

	routed := routeTo(t, zapcore.WarnLevel)

	// The plain core has no switch, so the route does not reach it.
	var plain, switched bytes.Buffer
	logAll(zap.New(newConsoleCore(&plain)))

	logger := zap.New(&switchCore{console: newConsoleCore(&switched)})
	logAll(logger)
	if switched.Len() != 0 {
		t.Errorf("a routed entry reached the console: %q", switched.String())
	}
	if n := len(routed.all()); n != 3 {
		t.Errorf("routed %d entries, want 3", n)
	}

	Route(nil)
	logAll(logger)
	if switched.String() != plain.String() {
		t.Errorf("console output changed:\n got %q\nwant %q", switched.String(), plain.String())
	}
	if plain.Len() == 0 {
		t.Fatal("the plain console core wrote nothing; the comparison proves nothing")
	}
}
