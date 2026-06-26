// Package sponsor shows occasional, throttled sponsorship messages to users.
package sponsor

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/env"
	"github.com/datamitsu/datamitsu/internal/ui"
)

const (
	sponsorActivationThreshold = 30
	minDaysBetweenShows        = 7
)

// Manager tracks per-user state and decides when to print a sponsor message.
type Manager struct {
	cacheDir string
	clock    Clock
	rnd      *rand.Rand
}

// New returns a Manager that persists its state under cacheDir and uses the real clock.
func New(cacheDir string) *Manager {
	return &Manager{
		cacheDir: cacheDir,
		clock:    realClock{},
		//nolint:gosec // G404: non-cryptographic use (selecting a rotating sponsor message); math/rand is intentional
		rnd: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NewWithClock returns a Manager backed by the supplied clock, for deterministic tests.
func NewWithClock(cacheDir string, clock Clock) *Manager {
	return &Manager{
		cacheDir: cacheDir,
		clock:    clock,
		//nolint:gosec // G404: non-cryptographic use (selecting a rotating sponsor message); math/rand is intentional
		rnd: rand.New(rand.NewSource(clock.Now().UnixNano())),
	}
}

// StaticLine returns a single, always-shown sponsorship line for non-throttled contexts.
func StaticLine() string {
	return "Support datamitsu development: " + sponsorURL
}

// MaybePrint prints a sponsor message if activation thresholds and throttling allow it.
func (m *Manager) MaybePrint(isJSONOutput bool) {
	defer func() { _ = recover() }()

	path := statePath(m.cacheDir)
	state, err := loadState(path)
	if err != nil {
		state = &State{}
	}

	// ui.Quiet() covers JSON-L mode: a sponsor line written to stderr would inject
	// a non-JSON line into the typed event stream. Returning before the counter
	// logic also avoids advancing activation state on a suppressed run.
	if env.NoSponsor() || isJSONOutput || env.IsCI() || ui.Quiet() {
		return
	}

	if !state.Activated {
		state.SuccessfulRuns++
		if state.SuccessfulRuns >= sponsorActivationThreshold {
			state.Activated = true
			state.LastShown = m.clock.Now()
			m.printMessage()
		}
	} else if m.clock.Now().Sub(state.LastShown) >= time.Duration(minDaysBetweenShows)*24*time.Hour {
		// On the first successful run at least minDaysBetweenShows after the last
		// show, reset activation and the counter. The message must then be earned
		// again by re-accumulating sponsorActivationThreshold successful runs, so
		// the cadence is jittered (≥7 days AND N more successful runs) and stays
		// unobtrusive — not a fixed weekly reminder, nor every Nth run.
		state.Activated = false
		state.SuccessfulRuns = 0
		state.LastShown = time.Time{}
	}

	_ = saveState(path, state)
}

func (m *Manager) printMessage() {
	msg := selectRandomMessage(m.rnd)
	coloredMsg := clr.Yellow(msg)
	fmt.Fprintf(os.Stderr, "\n%s\n", coloredMsg)
}
