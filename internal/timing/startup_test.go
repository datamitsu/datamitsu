package timing

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

// enableStartup turns startup instrumentation on for one test and guarantees a
// clean recorder before and after, so cases cannot leak phases into each other.
func enableStartup(t *testing.T) {
	t.Helper()
	t.Setenv("DATAMITSU_STARTUP_TIMINGS", "1")
	ResetStartupPhases()
	t.Cleanup(ResetStartupPhases)
}

func TestStartStartupPhaseRecordsNothingWhenDisabled(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
	}{
		{name: "unset"},
		{name: "zero", value: "0", set: true},
		{name: "empty", value: "", set: true},
		{name: "non-numeric", value: "yes", set: true},
		{name: "other number", value: "2", set: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("DATAMITSU_STARTUP_TIMINGS", tt.value)
			} else {
				t.Setenv("DATAMITSU_STARTUP_TIMINGS", "")
			}
			ResetStartupPhases()
			t.Cleanup(ResetStartupPhases)

			StartStartupPhase(PhaseLoadConfig)()

			if got := StartupPhases(); len(got) != 0 {
				t.Fatalf("recorded %d phases with the env var %q, want 0", len(got), tt.value)
			}

			var buf bytes.Buffer
			PrintStartup(&buf)
			if buf.Len() != 0 {
				t.Errorf("PrintStartup wrote %q, want nothing", buf.String())
			}
		})
	}
}

func TestStartStartupPhaseAggregatesByName(t *testing.T) {
	enableStartup(t)

	StartStartupPhase(PhaseEngineNew)()
	StartStartupPhase(PhaseEngineNew)()
	StartStartupPhase(PhaseLoadConfig)()

	phases := StartupPhases()
	if len(phases) != 2 {
		t.Fatalf("got %d phases, want 2: %+v", len(phases), phases)
	}

	// First-recorded order is preserved so the report is stable.
	if phases[0].Name != PhaseEngineNew || phases[1].Name != PhaseLoadConfig {
		t.Fatalf("unexpected order: %q, %q", phases[0].Name, phases[1].Name)
	}
	if phases[0].Count != 2 {
		t.Errorf("engine.New count = %d, want 2", phases[0].Count)
	}
	if phases[1].Count != 1 {
		t.Errorf("loadConfig count = %d, want 1", phases[1].Count)
	}
	if phases[0].Total < phases[0].Longest {
		t.Errorf("total %v < longest %v", phases[0].Total, phases[0].Longest)
	}
}

func TestPrintStartupIncludesEveryPhase(t *testing.T) {
	enableStartup(t)

	StartStartupPhase(PhaseGitRoot)()
	StartStartupPhase(PhaseStripTypes)()

	var buf bytes.Buffer
	PrintStartup(&buf)
	out := buf.String()

	for _, want := range []string{"Startup phases", PhaseGitRoot, PhaseStripTypes} {
		if !strings.Contains(out, want) {
			t.Errorf("PrintStartup output missing %q:\n%s", want, out)
		}
	}
}

// The report is emitted both when the config load finishes and at process exit,
// because commands that os.Exit never reach the latter. Printing must therefore
// happen at most once.
func TestPrintStartupPrintsOnlyOnce(t *testing.T) {
	enableStartup(t)

	StartStartupPhase(PhaseLoadConfig)()

	var first, second bytes.Buffer
	PrintStartup(&first)
	PrintStartup(&second)

	if first.Len() == 0 {
		t.Fatal("first PrintStartup wrote nothing, want the report")
	}
	if second.Len() != 0 {
		t.Errorf("second PrintStartup wrote %q, want nothing", second.String())
	}
}

func TestPrintStartupWritesNothingWithoutPhases(t *testing.T) {
	enableStartup(t)

	var buf bytes.Buffer
	PrintStartup(&buf)
	if buf.Len() != 0 {
		t.Errorf("PrintStartup wrote %q with no recorded phases, want nothing", buf.String())
	}
}

func TestStartupRecorderIsConcurrencySafe(t *testing.T) {
	enableStartup(t)

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range 10 {
				StartStartupPhase(PhaseEngineNew)()
				_ = StartupPhases()
			}
		}()
	}
	wg.Wait()

	phases := StartupPhases()
	if len(phases) != 1 {
		t.Fatalf("got %d phases, want 1: %+v", len(phases), phases)
	}
	if phases[0].Count != goroutines*10 {
		t.Errorf("count = %d, want %d", phases[0].Count, goroutines*10)
	}
}

func TestFormatStartupDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{in: 400 * time.Nanosecond, want: "400ns"},
		{in: 1500 * time.Nanosecond, want: "1.5µs"},
		{in: 2500 * time.Microsecond, want: "2.50ms"},
		{in: 1500 * time.Millisecond, want: "1.500s"},
	}

	for _, tt := range tests {
		if got := formatStartupDuration(tt.in); got != tt.want {
			t.Errorf("formatStartupDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
