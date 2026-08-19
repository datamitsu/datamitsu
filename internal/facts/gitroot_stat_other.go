//go:build !unix

package facts

// statIdentity reports nothing on platforms that expose no POSIX stat data.
//
// Windows is the one released target this covers. Ownership there is a security
// descriptor rather than a uid, and git compares it against the process token —
// reimplementing that is not something the startup path should guess at, so the
// pure-Go walk declines on those platforms and git answers instead. The cost is
// the subprocess the walk was written to avoid; the alternative is answering for
// repositories git would reject.
func statIdentity(string) (dirIdentity, bool) {
	return dirIdentity{}, false
}
