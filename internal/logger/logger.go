// Package logger provides a process-wide zap logger configured for
// human-readable, level-tagged CLI output (no timestamps or caller location)
// on stderr. The level is held in an atomic so it can be raised at runtime
// after flag parsing (e.g. by --verbose) without rebuilding the logger.
package logger

import (
	"os"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/env"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// atom holds the active log level. SetLevel adjusts it at runtime so loggers
// already derived via With() observe the change.
var atom = zap.NewAtomicLevelAt(env.GetLogLevel())

// Logger is the package-level zap logger; use it for all structured logging.
var Logger *zap.Logger

func init() {
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

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encCfg),
		zapcore.Lock(os.Stderr),
		atom,
	)
	Logger = zap.New(core)
}

// SetLevel adjusts the active log level at runtime (used by the --verbose flag).
func SetLevel(level zapcore.Level) {
	atom.SetLevel(level)
}

// Level returns the current active log level.
func Level() zapcore.Level {
	return atom.Level()
}
