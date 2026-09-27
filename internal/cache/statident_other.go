//go:build !linux && !darwin && !freebsd

package cache

import "os"

// No change time is reachable from a path-only stat here, so no stat comparison
// can rule out a same-length rewrite that restored the mtime. The zero value's
// Known flag is false, which makes every such comparison a miss: the bytes get
// read instead of trusted — see FileIdentity.
func statIdentity(os.FileInfo) FileIdentity { return FileIdentity{} }
