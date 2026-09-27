//go:build !windows

package tooling

import (
	"os/exec"
	"testing"
)

// A process that exited with a status of its own ended on its own, even if the
// stop signal reached it afterwards; one that died to a signal, or reports a
// signal the way a wrapper does, did not.
func TestEndedOnItsOwn(t *testing.T) {
	tests := []struct {
		script string
		want   bool
	}{
		{script: "exit 0", want: true},
		{script: "exit 7", want: true},
		{script: "kill -TERM $$", want: false},
		{script: "kill -KILL $$", want: false},
		{script: "exit 143", want: false},
		{script: "exit 130", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.script, func(t *testing.T) {
			cmd := exec.Command("/bin/sh", "-c", tt.script)
			_ = cmd.Run()
			if got := endedOnItsOwn(cmd.ProcessState); got != tt.want {
				t.Errorf("endedOnItsOwn(%q) = %v, want %v", tt.script, got, tt.want)
			}
		})
	}
	if endedOnItsOwn(nil) {
		t.Error("endedOnItsOwn(nil) = true, want false: a process that never ran did not end on its own")
	}
}
