package env

import (
	"github.com/datamitsu/datamitsu/internal/ldflags"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// envVar represents environment variable with its metadata
type envVar struct {
	Name         string
	DefaultValue string
	Description  string
}

func (e envVar) String() string {
	return e.Name
}

func getDefaultMaxWorkers() string {
	n := runtime.NumCPU() * 3 / 4
	if n < 4 {
		n = 4
	}
	if n > 16 {
		n = 16
	}
	return strconv.Itoa(n)
}

var (
	cacheDir = envVar{
		Name:        fmt.Sprintf("%s_CACHE_DIR", strings.ToUpper(ldflags.PackageName)),
		Description: "Custom cache directory path",
	}

	logLevel = envVar{
		Name:         fmt.Sprintf("%s_LOG_LEVEL", strings.ToUpper(ldflags.PackageName)),
		DefaultValue: "info",
		Description:  "Log level (debug, info, warn, error)",
	}

	maxCmdLength = envVar{
		Name:         fmt.Sprintf("%s_MAX_CMD_LENGTH", strings.ToUpper(ldflags.PackageName)),
		DefaultValue: "32000",
		Description:  "Maximum command line length for batch mode chunking",
	}

	maxErrorCommandDisplay = envVar{
		Name:         fmt.Sprintf("%s_MAX_ERROR_CMD_DISPLAY", strings.ToUpper(ldflags.PackageName)),
		DefaultValue: "120",
		Description:  "Maximum command length to display in error output (will be truncated with ...)",
	}

	maxParallelWorkers = envVar{
		Name:         fmt.Sprintf("%s_MAX_PARALLEL_WORKERS", strings.ToUpper(ldflags.PackageName)),
		DefaultValue: getDefaultMaxWorkers(),
		Description:  "Maximum number of parallel workers for task execution",
	}

	timings = envVar{
		Name:         fmt.Sprintf("%s_TIMINGS", strings.ToUpper(ldflags.PackageName)),
		DefaultValue: "0",
		Description:  "Enable detailed timing output for each stage (1=enabled, 0=disabled)",
	}

	concurrency = envVar{
		Name:         fmt.Sprintf("%s_CONCURRENCY", strings.ToUpper(ldflags.PackageName)),
		DefaultValue: "3",
		Description:  "Number of concurrent binary downloads during init",
	}

	noSponsor = envVar{
		Name:         fmt.Sprintf("%s_NO_SPONSOR", strings.ToUpper(ldflags.PackageName)),
		DefaultValue: "",
		Description:  "Disable sponsor messages (set to any non-empty value)",
	}

	binaryCommandOverride = envVar{
		Name:         fmt.Sprintf("%s_BINARY_COMMAND", strings.ToUpper(ldflags.PackageName)),
		DefaultValue: "",
		Description:  "Override binary command path (used in facts collection)",
	}
)
