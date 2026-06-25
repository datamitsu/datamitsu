package env

import (
	"runtime"
	"strconv"
	"strings"

	"github.com/datamitsu/datamitsu/internal/ldflags"
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
	n := min(max(runtime.NumCPU()*3/4, 4), 16)
	return strconv.Itoa(n)
}

var (
	cacheDir = envVar{
		Name:        strings.ToUpper(ldflags.PackageName) + "_CACHE_DIR",
		Description: "Custom cache directory path",
	}

	logLevel = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_LOG_LEVEL",
		DefaultValue: "warn",
		Description:  "Log level (debug, info, warn, error); default warn, use --verbose for debug",
	}

	maxCmdLength = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_MAX_CMD_LENGTH",
		DefaultValue: "32000",
		Description:  "Maximum command line length for batch mode chunking",
	}

	maxErrorCommandDisplay = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_MAX_ERROR_CMD_DISPLAY",
		DefaultValue: "120",
		Description:  "Maximum command length to display in error output (will be truncated with ...)",
	}

	maxParallelWorkers = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_MAX_PARALLEL_WORKERS",
		DefaultValue: getDefaultMaxWorkers(),
		Description:  "Maximum number of parallel workers for task execution",
	}

	timings = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_TIMINGS",
		DefaultValue: "0",
		Description:  "Enable detailed timing output for each stage (1=enabled, 0=disabled)",
	}

	concurrency = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_CONCURRENCY",
		DefaultValue: "3",
		Description:  "Number of concurrent binary downloads during init",
	}

	noSponsor = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_NO_SPONSOR",
		DefaultValue: "",
		Description:  "Disable sponsor messages (set to any non-empty value)",
	}

	binaryCommandOverride = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_BINARY_COMMAND",
		DefaultValue: "",
		Description:  "Override binary command path (used in facts collection)",
	}

	installTimeout = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_INSTALL_TIMEOUT",
		DefaultValue: "600",
		Description:  "Per-app install timeout in seconds (0=disabled)",
	}

	minimumReleaseAge = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_MIN_RELEASE_AGE",
		DefaultValue: "10080",
		Description:  "Minimum release age in minutes for supply-chain filtering (0=disabled)",
	}

	ociRegistry = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_OCI_REGISTRY",
		DefaultValue: "ghcr.io",
		Description:  "OCI registry host used to resolve the base image digest for generated Dockerfiles",
	}

	offline = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_OFFLINE",
		DefaultValue: "",
		Description:  "Refuse all network access (set to any non-empty value); requires a pre-seeded store",
	}

	noOCI = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_NO_OCI",
		DefaultValue: "",
		Description:  "Disable OCI bundle store seeding (set to any non-empty value)",
	}

	libcOverride = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_LIBC",
		DefaultValue: "",
		Description:  "Override host libc detection (glibc or musl); affects store paths and OCI bundle selection",
	}

	parsersDir = envVar{
		Name:         strings.ToUpper(ldflags.PackageName) + "_PARSERS_DIR",
		DefaultValue: "",
		Description:  "Override directory for downloaded WASM parser modules (default {store}/.parsers)",
	}
)
