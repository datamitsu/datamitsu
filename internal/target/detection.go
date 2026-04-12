package target

// DetectLibc detects the libc implementation on the current Linux system.
// Returns LibcGlibc, LibcMusl, or LibcUnknown.
// On non-Linux systems, callers should not call this; use DetectHost() instead.
//
// Multi-stage detection priority:
//  1. ldd --version output parsing
//  2. ELF interpreter (PT_INTERP) from current binary
//  3. Loader path globbing (/lib/ld-musl-*, /lib/ld-linux-*)
//  4. Returns LibcUnknown if all stages fail
func DetectLibc() LibcType {
	if result := detectViaLdd(); result != LibcUnknown {
		return result
	}
	if result := detectViaELF(); result != LibcUnknown {
		return result
	}
	if result := detectViaLoaderPaths(); result != LibcUnknown {
		return result
	}
	return LibcUnknown
}
