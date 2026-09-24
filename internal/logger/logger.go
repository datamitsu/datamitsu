// Package logger provides a process-wide zap logger configured for
// human-readable, level-tagged CLI output (no timestamps or caller location)
// on stderr. The level is held in an atomic so it can be raised at runtime
// after flag parsing (e.g. by --verbose) without rebuilding the logger.
//
// When stderr is a JSON-L event stream, Route hands every entry to that stream
// as a log event instead: a plain-text line there breaks every consumer that
// parses it.
package logger

import (
	"fmt"
	"io"
	"os"
	"slices"
	"sync/atomic"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/uievent"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// atom holds the active log level. SetLevel adjusts it at runtime so loggers
// already derived via With() observe the change.
var atom = zap.NewAtomicLevelAt(env.GetLogLevel())

// route, when set, receives entries in place of the console.
var route atomic.Pointer[func(uievent.Event)]

// Logger is the package-level zap logger; use it for all structured logging.
var Logger *zap.Logger

func init() {
	Logger = zap.New(&switchCore{console: newConsoleCore(zapcore.Lock(os.Stderr))})
}

func newConsoleCore(w io.Writer) zapcore.Core {
	encCfg := zap.NewProductionEncoderConfig()
	// CLI output, not a server log: drop timestamp and caller so messages read
	// cleanly. A single space separates the level tag, message and any fields.
	encCfg.TimeKey = ""
	encCfg.CallerKey = ""
	encCfg.ConsoleSeparator = " "
	if clr.Enabled() {
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		encCfg.EncodeLevel = zapcore.CapitalLevelEncoder
	}
	return zapcore.NewCore(zapcore.NewConsoleEncoder(encCfg), zapcore.AddSync(w), atom)
}

// SetLevel adjusts the active log level at runtime (used by the --verbose flag).
func SetLevel(level zapcore.Level) {
	atom.SetLevel(level)
}

// Level returns the current active log level.
func Level() zapcore.Level {
	return atom.Level()
}

// Route hands every entry the active level lets through to emit as a log event
// instead of writing it to the console; nil restores the console. It applies to
// every logger, including ones derived before the call.
func Route(emit func(uievent.Event)) {
	if emit == nil {
		route.Store(nil)
		return
	}
	route.Store(&emit)
}

// switchCore writes to the console unless a route is installed. Loggers derived
// with With hold a core of their own — several live in package variables built
// at init — so the route is read on every write, never swapped into one core.
type switchCore struct {
	console zapcore.Core
	fields  []zapcore.Field // accumulated by With, for the routed message
}

func (c *switchCore) Enabled(level zapcore.Level) bool { return c.console.Enabled(level) }

// Level lets zap report the active level through this core.
func (c *switchCore) Level() zapcore.Level { return zapcore.LevelOf(c.console) }

func (c *switchCore) With(fields []zapcore.Field) zapcore.Core {
	return &switchCore{
		console: c.console.With(fields),
		fields:  append(slices.Clip(c.fields), fields...),
	}
}

func (c *switchCore) Check(ent zapcore.Entry, ce *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	if c.Enabled(ent.Level) {
		return ce.AddCore(ent, c)
	}
	return ce
}

func (c *switchCore) Write(ent zapcore.Entry, fields []zapcore.Field) error {
	if emit := route.Load(); emit != nil {
		(*emit)(uievent.Event{
			Type:  uievent.TypeLog,
			OpID:  uievent.NextOpID("log"),
			Level: eventLevel(ent.Level),
			Msg:   routedMessage(ent.Message, c.fields, fields),
		})
		return nil
	}
	if err := c.console.Write(ent, fields); err != nil {
		return fmt.Errorf("write log entry: %w", err)
	}
	return nil
}

func (c *switchCore) Sync() error {
	if err := c.console.Sync(); err != nil {
		return fmt.Errorf("sync log output: %w", err)
	}
	return nil
}

func eventLevel(level zapcore.Level) string {
	switch {
	case level <= zapcore.DebugLevel:
		return uievent.LevelDebug
	case level == zapcore.InfoLevel:
		return uievent.LevelInfo
	case level == zapcore.WarnLevel:
		return uievent.LevelWarn
	default:
		return uievent.LevelError
	}
}

// fieldEncoderConfig encodes fields only: every entry key is empty.
var fieldEncoderConfig = zapcore.EncoderConfig{
	EncodeDuration: zapcore.StringDurationEncoder,
	EncodeTime:     zapcore.ISO8601TimeEncoder,
	SkipLineEnding: true,
}

// routedMessage is the entry message followed by its fields — With's first, then
// the call's — as one compact JSON object in the order they were added, as the
// console line shows them. A namespace no field lands in is left out.
func routedMessage(msg string, with, fields []zapcore.Field) string {
	enc := zapcore.NewJSONEncoder(fieldEncoderConfig)
	var pending []zapcore.Field
	added := 0
	for _, group := range [][]zapcore.Field{with, fields} {
		for _, f := range group {
			if f.Type == zapcore.SkipType {
				continue
			}
			if f.Type == zapcore.NamespaceType {
				pending = append(pending, f)
				continue
			}
			for _, ns := range pending {
				ns.AddTo(enc)
			}
			pending = pending[:0]
			f.AddTo(enc)
			added++
		}
	}
	if added == 0 {
		return msg
	}
	buf, err := enc.EncodeEntry(zapcore.Entry{}, nil)
	if err != nil {
		return msg
	}
	defer buf.Free()
	return msg + " " + buf.String()
}
