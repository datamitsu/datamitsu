package logger

import (
	"testing"

	"go.uber.org/zap"
)

func TestLoggerInitialized(t *testing.T) {
	if Logger == nil {
		t.Fatal("Logger is nil, should be initialized")
	}
}

func TestLoggerIsZapLogger(t *testing.T) {
	if Logger == nil {
		t.Fatal("Logger is nil")
	}

	if _, ok := interface{}(Logger).(*zap.Logger); !ok {
		t.Error("Logger is not a *zap.Logger")
	}
}

func TestLoggerCanLog(t *testing.T) {
	if Logger == nil {
		t.Fatal("Logger is nil")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Logger.Info() panicked: %v", r)
		}
	}()

	Logger.Info("test message")
	Logger.Debug("debug message")
	Logger.Warn("warn message")
	Logger.Error("error message")
}

func TestLoggerWithFields(t *testing.T) {
	if Logger == nil {
		t.Fatal("Logger is nil")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Logger with fields panicked: %v", r)
		}
	}()

	Logger.Info("test with fields",
		zap.String("key", "value"),
		zap.Int("count", 42),
	)
}
