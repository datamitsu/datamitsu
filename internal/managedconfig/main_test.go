package managedconfig

import (
	"testing"

	"github.com/datamitsu/datamitsu/internal/gittest"
)

func TestMain(m *testing.M) {
	gittest.Isolate()
	m.Run()
}
