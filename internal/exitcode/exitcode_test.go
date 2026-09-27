package exitcode

import (
	"errors"
	"fmt"
	"io/fs"
	"testing"
)

type coded interface {
	error
	ExitCode() int
}

// Each error keeps its message and cause and carries its code through any
// wrapping, which is how the exit site in cmd finds it.
func TestErrorsCarryTheirCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		msg  string
	}{
		{name: "usage", err: UsageErrorf("invalid --widen-to value: %s", "Repo"), code: Usage, msg: "invalid --widen-to value: Repo"},
		{name: "usage wrapping", err: UsageError{Err: fmt.Errorf("parse: %w", fs.ErrInvalid)}, code: Usage, msg: "parse: invalid argument"},
		{name: "coverage", err: CoverageErrorf("--require-coverage=repo: %s", "narrowed"), code: Coverage, msg: "--require-coverage=repo: narrowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapped := fmt.Errorf("run: %w", tt.err)
			got, ok := errors.AsType[coded](wrapped)
			if !ok {
				t.Fatalf("%v carries no exit code", wrapped)
			}
			if got.ExitCode() != tt.code {
				t.Errorf("ExitCode() = %d, want %d", got.ExitCode(), tt.code)
			}
			if tt.err.Error() != tt.msg {
				t.Errorf("Error() = %q, want %q", tt.err.Error(), tt.msg)
			}
		})
	}
	if !errors.Is(UsageError{Err: fmt.Errorf("x: %w", fs.ErrInvalid)}, fs.ErrInvalid) {
		t.Error("UsageError hides its cause from errors.Is")
	}
}

func TestCodes(t *testing.T) {
	if Usage != 2 || Coverage != 4 {
		t.Errorf("Usage = %d, Coverage = %d; the documented codes are 2 and 4", Usage, Coverage)
	}
}
