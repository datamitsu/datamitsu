package logger

import (
	"strings"
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

	if _, ok := any(Logger).(*zap.Logger); !ok {
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

// TestConsoleFilter: an installed filter rewrites what the console writes;
// removing it writes entries as they are.
func TestConsoleFilter(t *testing.T) {
	var out strings.Builder
	w := filteredWriter{&out}
	SetConsoleFilter(func(s string) string { return strings.ReplaceAll(s, "##vso[", "##vso [") })
	t.Cleanup(func() { SetConsoleFilter(nil) })
	if n, err := w.Write([]byte("DEBUG output ##vso[task.complete]\n")); err != nil || n != 34 {
		t.Fatalf("Write = %d, %v", n, err)
	}
	SetConsoleFilter(nil)
	_, _ = w.Write([]byte("##vso[x]\n"))
	if got := out.String(); got != "DEBUG output ##vso [task.complete]\n##vso[x]\n" {
		t.Errorf("console = %q", got)
	}
}
