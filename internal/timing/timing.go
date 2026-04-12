package timing

import (
	"github.com/datamitsu/datamitsu/internal/env"
	"fmt"
	"sync"
	"time"
)

// Timings tracks execution times for different stages
type Timings struct {
	mu      sync.Mutex
	enabled bool
	stages  []Stage
	started time.Time
}

// Stage represents a single timed stage with optional children
type Stage struct {
	Name     string
	Duration time.Duration
	Children []Stage
}

// New creates a new Timings instance
func New() *Timings {
	return &Timings{
		enabled: env.IsTimingsEnabled(),
		stages:  make([]Stage, 0),
		started: time.Now(),
	}
}

// IsEnabled returns whether timing is enabled
func (t *Timings) IsEnabled() bool {
	return t.enabled
}

// Start begins timing a new stage and returns a function to end it
// Usage: defer timings.Start("stage name")()
func (t *Timings) Start(name string) func() {
	if !t.enabled {
		return func() {}
	}

	start := time.Now()

	return func() {
		duration := time.Since(start)
		t.mu.Lock()
		defer t.mu.Unlock()
		t.stages = append(t.stages, Stage{
			Name:     name,
			Duration: duration,
		})
	}
}

// StartWithChildren begins timing a stage that may have child stages
// Returns a ChildTimings object that can track sub-stages
func (t *Timings) StartWithChildren(name string) *ChildTimings {
	if !t.enabled {
		return &ChildTimings{enabled: false}
	}

	return &ChildTimings{
		enabled:  true,
		parent:   t,
		name:     name,
		started:  time.Now(),
		children: make([]Stage, 0),
	}
}

// Print outputs the timing information in a hierarchical format
func (t *Timings) Print() {
	if !t.enabled {
		return
	}
	if len(t.stages) == 0 {
		return
	}

	totalDuration := time.Since(t.started)

	fmt.Println()
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("⏱  Timing Statistics")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	for _, stage := range t.stages {
		printStage(stage, 0)
	}

	fmt.Println("───────────────────────────────────────────────────────────────")
	fmt.Printf("Total: %s\n", formatDuration(totalDuration))
	fmt.Println("───────────────────────────────────────────────────────────────")
}

// ChildTimings tracks timing for a stage with potential child stages
type ChildTimings struct {
	mu       sync.Mutex
	enabled  bool
	parent   *Timings
	name     string
	started  time.Time
	children []Stage
}

// StartChild begins timing a child stage
// Usage: defer childTimings.StartChild("child name")()
func (c *ChildTimings) StartChild(name string) func() {
	if !c.enabled {
		return func() {}
	}

	start := time.Now()

	return func() {
		duration := time.Since(start)
		c.mu.Lock()
		defer c.mu.Unlock()
		c.children = append(c.children, Stage{
			Name:     name,
			Duration: duration,
		})
	}
}

// End finishes timing this stage and adds it to the parent
func (c *ChildTimings) End() {
	if !c.enabled {
		return
	}

	duration := time.Since(c.started)
	c.parent.mu.Lock()
	defer c.parent.mu.Unlock()

	c.parent.stages = append(c.parent.stages, Stage{
		Name:     c.name,
		Duration: duration,
		Children: c.children,
	})
}

// printStage recursively prints a stage and its children
func printStage(stage Stage, indent int) {
	prefix := ""
	for i := 0; i < indent; i++ {
		prefix += "  "
	}

	// Format the stage name and duration
	fmt.Printf("%s%s: %s\n", prefix, stage.Name, formatDuration(stage.Duration))

	// Print children if any
	for _, child := range stage.Children {
		printStage(child, indent+1)
	}
}

// formatDuration formats a duration for display
func formatDuration(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.2fs", d.Seconds())
}
