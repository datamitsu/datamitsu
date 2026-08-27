//go:build !linux && !darwin && !freebsd

package tooling

import "os"

// No change time is reachable from a path-only stat here, so no stat comparison
// can rule out a same-length rewrite that restored the mtime. The zero value's
// known flag is false, which makes every such comparison a miss: the bytes get
// read instead of trusted — see fileIdent.
func statIdent(os.FileInfo) fileIdent { return fileIdent{} }
