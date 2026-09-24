package lsp

import (
	"errors"
	"testing"
	"time"
)

func TestRetryTransient(t *testing.T) {
	transientErr, fatalErr := errors.New("sharing violation"), errors.New("no such file")
	transient := func(err error) bool { return errors.Is(err, transientErr) }
	tests := []struct {
		name      string
		failures  []error
		limit     time.Duration
		wantErr   error
		wantCalls int
	}{
		{name: "succeeds at once", wantCalls: 1},
		{name: "retries a transient failure", failures: []error{transientErr, transientErr}, limit: time.Second, wantCalls: 3},
		{name: "never retries another error", failures: []error{fatalErr}, limit: time.Second, wantErr: fatalErr, wantCalls: 1},
		{name: "gives up at the limit", failures: []error{transientErr, transientErr}, wantErr: transientErr, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			err := retryTransient(func() error {
				calls++
				if calls <= len(tt.failures) {
					return tt.failures[calls-1]
				}
				return nil
			}, transient, tt.limit)
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Errorf("%d calls, want %d", calls, tt.wantCalls)
			}
		})
	}
}
