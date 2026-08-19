//go:build windows

package sourcefarm

import "io/fs"

// ownerUID has no meaning on Windows, where access is decided by ACLs rather
// than by a numeric owner. The caller skips the ownership check.
func ownerUID(fs.FileInfo) (int, bool) { return 0, false }
