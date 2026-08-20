//go:build windows

package sourcefarm

import "os"

// Windows has no flock. The bake's correctness does not depend on the lock:
// the farm is assembled in a staging directory and renamed into place, so two
// concurrent bakers duplicate work rather than corrupt the result. Treating the
// lock as always-available keeps that behavior without a second code path.

func tryLockFile(*os.File) (bool, error) { return true, nil }

func lockFile(*os.File) error { return nil }

func unlockFile(*os.File) {}
