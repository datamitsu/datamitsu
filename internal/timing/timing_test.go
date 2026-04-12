package timing

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	t.Run("enabled when env variable is set", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		timings := New()
		if !timings.IsEnabled() {
			t.Error("expected timings to be enabled")
		}
	})

	t.Run("disabled when env variable is not set", func(t *testing.T) {
		_ = os.Unsetenv("DATAMITSU_TIMINGS")

		timings := New()
		if timings.IsEnabled() {
			t.Error("expected timings to be disabled")
		}
	})

	t.Run("stages initialized", func(t *testing.T) {
		timings := New()
		if timings.stages == nil {
			t.Error("stages should be initialized")
		}
	})
}

func TestIsEnabled(t *testing.T) {
	t.Run("returns enabled state", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		timings := New()
		if !timings.IsEnabled() {
			t.Error("expected IsEnabled() to return true")
		}

		_ = os.Unsetenv("DATAMITSU_TIMINGS")
		timings2 := New()
		if timings2.IsEnabled() {
			t.Error("expected IsEnabled() to return false")
		}
	})
}

func TestStart(t *testing.T) {
	t.Run("records duration when enabled", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		timings := New()
		end := timings.Start("test stage")
		time.Sleep(10 * time.Millisecond)
		end()

		if len(timings.stages) != 1 {
			t.Errorf("expected 1 stage, got %d", len(timings.stages))
		}

		if timings.stages[0].Name != "test stage" {
			t.Errorf("expected stage name 'test stage', got '%s'", timings.stages[0].Name)
		}

		if timings.stages[0].Duration < 10*time.Millisecond {
			t.Errorf("expected duration >= 10ms, got %v", timings.stages[0].Duration)
		}
	})

	t.Run("does not record when disabled", func(t *testing.T) {
		_ = os.Unsetenv("DATAMITSU_TIMINGS")

		timings := New()
		end := timings.Start("test stage")
		time.Sleep(10 * time.Millisecond)
		end()

		if len(timings.stages) != 0 {
			t.Errorf("expected 0 stages when disabled, got %d", len(timings.stages))
		}
	})

	t.Run("defer pattern works", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		timings := New()

		func() {
			defer timings.Start("deferred stage")()
			time.Sleep(10 * time.Millisecond)
		}()

		if len(timings.stages) != 1 {
			t.Errorf("expected 1 stage, got %d", len(timings.stages))
		}
	})
}

func TestStartWithChildren(t *testing.T) {
	t.Run("records parent and children when enabled", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		timings := New()
		child := timings.StartWithChildren("parent stage")

		time.Sleep(5 * time.Millisecond)
		endChild1 := child.StartChild("child 1")
		time.Sleep(5 * time.Millisecond)
		endChild1()

		endChild2 := child.StartChild("child 2")
		time.Sleep(5 * time.Millisecond)
		endChild2()

		child.End()

		if len(timings.stages) != 1 {
			t.Errorf("expected 1 parent stage, got %d", len(timings.stages))
		}

		parent := timings.stages[0]
		if parent.Name != "parent stage" {
			t.Errorf("expected parent name 'parent stage', got '%s'", parent.Name)
		}

		if len(parent.Children) != 2 {
			t.Errorf("expected 2 children, got %d", len(parent.Children))
		}

		if parent.Children[0].Name != "child 1" {
			t.Errorf("expected first child 'child 1', got '%s'", parent.Children[0].Name)
		}

		if parent.Children[1].Name != "child 2" {
			t.Errorf("expected second child 'child 2', got '%s'", parent.Children[1].Name)
		}
	})

	t.Run("does not record when disabled", func(t *testing.T) {
		_ = os.Unsetenv("DATAMITSU_TIMINGS")

		timings := New()
		child := timings.StartWithChildren("parent stage")

		endChild := child.StartChild("child")
		endChild()
		child.End()

		if len(timings.stages) != 0 {
			t.Errorf("expected 0 stages when disabled, got %d", len(timings.stages))
		}
	})
}

func TestPrint(t *testing.T) {
	t.Run("prints timing statistics when enabled", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		timings := New()
		end := timings.Start("test stage")
		time.Sleep(100 * time.Millisecond)
		end()

		timings.Print()

		_ = w.Close()
		os.Stdout = old

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		output := buf.String()

		if !strings.Contains(output, "Timing Statistics") {
			t.Error("output should contain 'Timing Statistics'")
		}

		if !strings.Contains(output, "test stage") {
			t.Error("output should contain stage name")
		}

		if !strings.Contains(output, "ms") && !strings.Contains(output, "s") {
			t.Error("output should contain duration")
		}
	})

	t.Run("does not print when disabled", func(t *testing.T) {
		_ = os.Unsetenv("DATAMITSU_TIMINGS")

		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		timings := New()
		timings.Print()

		_ = w.Close()
		os.Stdout = old

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		output := buf.String()

		if len(output) > 0 {
			t.Error("expected no output when disabled")
		}
	})

	t.Run("does not print when no stages", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		timings := New()
		timings.Print()

		_ = w.Close()
		os.Stdout = old

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		output := buf.String()

		if len(output) > 0 {
			t.Error("expected no output when no stages")
		}
	})

	t.Run("prints hierarchical output with children", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		old := os.Stdout
		r, w, _ := os.Pipe()
		os.Stdout = w

		timings := New()
		child := timings.StartWithChildren("parent")
		endChild := child.StartChild("child")
		time.Sleep(10 * time.Millisecond)
		endChild()
		child.End()

		timings.Print()

		_ = w.Close()
		os.Stdout = old

		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		output := buf.String()

		if !strings.Contains(output, "parent") {
			t.Error("output should contain parent stage")
		}

		if !strings.Contains(output, "child") {
			t.Error("output should contain child stage")
		}

		parentIdx := strings.Index(output, "parent")
		childIdx := strings.Index(output, "child")
		if childIdx <= parentIdx {
			t.Error("child should appear after parent in output")
		}
	})
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		contains string
	}{
		{
			name:     "milliseconds under 1 second",
			duration: 500 * time.Millisecond,
			contains: "ms",
		},
		{
			name:     "seconds",
			duration: 2 * time.Second,
			contains: "s",
		},
		{
			name:     "exact 1 second",
			duration: 1000 * time.Millisecond,
			contains: "s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("formatDuration(%v) = %s, should contain %s", tt.duration, result, tt.contains)
			}
		})
	}
}

func TestChildTimingsStartChild(t *testing.T) {
	t.Run("records child duration", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		timings := New()
		child := timings.StartWithChildren("parent")

		end := child.StartChild("test child")
		time.Sleep(10 * time.Millisecond)
		end()

		if len(child.children) != 1 {
			t.Errorf("expected 1 child, got %d", len(child.children))
		}

		if child.children[0].Duration < 10*time.Millisecond {
			t.Errorf("expected duration >= 10ms, got %v", child.children[0].Duration)
		}
	})

	t.Run("does not record when disabled", func(t *testing.T) {
		_ = os.Unsetenv("DATAMITSU_TIMINGS")

		timings := New()
		child := timings.StartWithChildren("parent")

		end := child.StartChild("test child")
		time.Sleep(10 * time.Millisecond)
		end()

		if len(child.children) != 0 {
			t.Errorf("expected 0 children when disabled, got %d", len(child.children))
		}
	})
}

func TestChildTimingsEnd(t *testing.T) {
	t.Run("adds parent to timings with children", func(t *testing.T) {
		_ = os.Setenv("DATAMITSU_TIMINGS", "1")
		defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

		timings := New()
		child := timings.StartWithChildren("parent")

		endChild := child.StartChild("child 1")
		endChild()

		child.End()

		if len(timings.stages) != 1 {
			t.Errorf("expected 1 stage, got %d", len(timings.stages))
		}

		if len(timings.stages[0].Children) != 1 {
			t.Errorf("expected 1 child in parent, got %d", len(timings.stages[0].Children))
		}
	})
}

func ExampleTimings_Start() {
	_ = os.Setenv("DATAMITSU_TIMINGS", "1")
	defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

	timings := New()
	defer timings.Start("example operation")()

	time.Sleep(10 * time.Millisecond)
	fmt.Println("Operation complete")
}

func ExampleTimings_StartWithChildren() {
	_ = os.Setenv("DATAMITSU_TIMINGS", "1")
	defer func() { _ = os.Unsetenv("DATAMITSU_TIMINGS") }()

	timings := New()
	child := timings.StartWithChildren("parent operation")
	defer child.End()

	for i := 0; i < 3; i++ {
		defer child.StartChild(fmt.Sprintf("sub-operation %d", i))()
		time.Sleep(5 * time.Millisecond)
	}
}
