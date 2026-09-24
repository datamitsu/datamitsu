package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/datamitsu/datamitsu/internal/ui"
	"github.com/datamitsu/datamitsu/internal/uievent"

	"github.com/spf13/cobra"
)

func TestSilenceUsageAndErrors(t *testing.T) {
	t.Run("SilenceUsage is set", func(t *testing.T) {
		if !rootCmd.SilenceUsage {
			t.Error("rootCmd.SilenceUsage should be true")
		}
	})

	t.Run("SilenceErrors is set", func(t *testing.T) {
		if !rootCmd.SilenceErrors {
			t.Error("rootCmd.SilenceErrors should be true")
		}
	})
}

func TestSilenceUsagePreventsUsageOnRuntimeError(t *testing.T) {
	// Create a test command that mimics our setup
	testRoot := &cobra.Command{
		Use:           "test",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	testRoot.AddCommand(&cobra.Command{
		Use: "failing-sub",
		RunE: func(cmd *cobra.Command, args []string) error {
			return errors.New("runtime error occurred")
		},
	})

	var stdout, stderr bytes.Buffer
	testRoot.SetOut(&stdout)
	testRoot.SetErr(&stderr)
	testRoot.SetArgs([]string{"failing-sub"})

	err := testRoot.Execute()
	if err == nil {
		t.Fatal("expected error from failing subcommand")
	}

	combined := stdout.String() + stderr.String()
	if strings.Contains(combined, "Usage:") {
		t.Errorf("SilenceUsage should prevent 'Usage:' from appearing on runtime errors, got: %q", combined)
	}
	if strings.Contains(combined, "runtime error occurred") {
		t.Errorf("SilenceErrors should prevent error message from appearing in cobra output, got: %q", combined)
	}
}

type eventRecorder struct {
	mu     sync.Mutex
	events []uievent.Event
}

func (r *eventRecorder) Emit(e uievent.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// A diagnostic report on a JSON-L stream is one log event, not lines of text
// that no consumer of the stream can parse.
func TestReportToStderrIsOneLogEventInJSONLMode(t *testing.T) {
	sink := &eventRecorder{}
	ui.SetEventSink(sink, true)
	t.Cleanup(func() { ui.SetEventSink(nil, false) })

	reportToStderr(func(w io.Writer) { _, _ = io.WriteString(w, "\n⏱  report\n  row one\n  row two\n") })
	reportToStderr(func(io.Writer) {})

	if len(sink.events) != 1 {
		t.Fatalf("events = %+v, want one for the report and none for an empty one", sink.events)
	}
	e := sink.events[0]
	if e.Type != uievent.TypeLog || e.Level != uievent.LevelInfo || !strings.HasPrefix(e.OpID, "log-") {
		t.Errorf("event = %+v, want an info log event with a log- op id", e)
	}
	if want := "⏱  report\n  row one\n  row two"; e.Msg != want {
		t.Errorf("msg = %q, want %q", e.Msg, want)
	}
}
