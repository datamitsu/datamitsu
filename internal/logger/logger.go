package logger

import (
	"github.com/datamitsu/datamitsu/internal/env"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

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
