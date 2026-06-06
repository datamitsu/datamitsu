package sponsor

import "time"

// Clock abstracts the current time so the sponsor logic can be tested deterministically.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}
