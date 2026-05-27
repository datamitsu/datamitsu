package runtimemanager

import (
	"encoding/json"
	"fmt"
)

// goLockFile is the JSON wrapper persisted (after brotli compression) as a
// Go app's lockFile. It carries the full go.mod and go.sum so the app can be
// rebuilt deterministically with `go build -mod=readonly`, where any go.sum
// mismatch fails the build — the supply chain guarantee for Go apps.
type goLockFile struct {
	Mod string `json:"mod"`
	Sum string `json:"sum"`
}

// parseGoLockFile decodes the JSON wrapper (already decompressed) into the
// go.mod and go.sum contents. Both fields are mandatory: go.mod identifies the
// module graph and go.sum carries the cryptographic checksums that make the
// build verifiable. A missing or empty field is treated as a configuration
// error rather than silently building without verification.
func parseGoLockFile(lockFile string) (goMod, goSum string, err error) {
	var lf goLockFile
	if err := json.Unmarshal([]byte(lockFile), &lf); err != nil {
		return "", "", fmt.Errorf("parse go lock file: %w", err)
	}
	if lf.Mod == "" {
		return "", "", fmt.Errorf("go lock file missing mod (go.mod) content")
	}
	if lf.Sum == "" {
		return "", "", fmt.Errorf("go lock file missing sum (go.sum) content")
	}
	return lf.Mod, lf.Sum, nil
}

// buildGoLockFileJSON marshals go.mod and go.sum into the JSON wrapper. The
// result is intended to be passed to CompressLockFile before being stored in
// config.
func buildGoLockFileJSON(goMod, goSum string) (string, error) {
	data, err := json.Marshal(goLockFile{Mod: goMod, Sum: goSum})
	if err != nil {
		return "", fmt.Errorf("build go lock file JSON: %w", err)
	}
	return string(data), nil
}
