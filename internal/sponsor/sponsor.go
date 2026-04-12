package sponsor

import (
	"fmt"
	"math/rand"
	"os"
	"time"

	clr "github.com/datamitsu/datamitsu/internal/color"
	"github.com/datamitsu/datamitsu/internal/env"
)

const (
	sponsorActivationThreshold = 30
	minDaysBetweenShows        = 7
)

type Manager struct {
	cacheDir string
	clock    Clock
	rnd      *rand.Rand
}

func New(cacheDir string) *Manager {
	return &Manager{
		cacheDir: cacheDir,
		clock:    realClock{},
		rnd:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func NewWithClock(cacheDir string, clock Clock) *Manager {
	return &Manager{
		cacheDir: cacheDir,
		clock:    clock,
		rnd:      rand.New(rand.NewSource(clock.Now().UnixNano())),
	}
}

func StaticLine() string {
	return "Support datamitsu development: " + sponsorURL
}

func (m *Manager) printMessage() {
	msg := selectRandomMessage(m.rnd)
	coloredMsg := clr.Yellow(msg)
	fmt.Fprintf(os.Stderr, "\n%s\n", coloredMsg)
}

func (m *Manager) MaybePrint(isJSONOutput bool) {
	defer func() { _ = recover() }()

	path := statePath(m.cacheDir)
	state, err := loadState(path)
	if err != nil {
		state = &State{}
	}

	if env.NoSponsor() || isJSONOutput || env.IsCI() {
		return
	}

	if !state.Activated {
		state.SuccessfulRuns++
		if state.SuccessfulRuns >= sponsorActivationThreshold {
			state.Activated = true
			state.LastShown = m.clock.Now()
			m.printMessage()
		}
	} else {
		if m.clock.Now().Sub(state.LastShown) >= time.Duration(minDaysBetweenShows)*24*time.Hour {
			state.Activated = false
			state.SuccessfulRuns = 0
			state.LastShown = time.Time{}
		}
	}

	_ = saveState(path, state)
}

