// Package logger provides a process-wide zap logger configured from the
// environment for console output with colored levels.
package logger

import (
	"github.com/datamitsu/datamitsu/internal/env"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the package-level zap logger initialized at startup from the
// configured log level; use it for all structured logging.
var Logger *zap.Logger

func init() {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	config.Encoding = "console"
	config.Level = zap.NewAtomicLevelAt(env.GetLogLevel())

	var err error

	Logger, err = config.Build()
	if err != nil {
		panic(err)
	}
}
