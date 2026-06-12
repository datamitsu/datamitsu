package config

import (
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/hashutil"
)

// chainHashPrefix is the canonical algorithm prefix for an expectChainHash pin.
const chainHashPrefix = "xxh3:"

// ChainHashMismatch is one root-layer expectChainHash pin that did not match the
// content entering the root layer.
type ChainHashMismatch struct {
	FileName string
	Expected string // normalized "xxh3:<hex>"
	Actual   string // "xxh3:<hex>"
	Incoming string // the content that entered the root layer (what was hashed)
}

// incomingToRootLayer returns the content entering the root (topmost) layer for a
// file: the last generated content strictly before the final layer entry, falling
// back to the original on-disk content when no upstream layer produced any. The
// final entry is the root layer's own output and is intentionally excluded — the
// pin is verified against the root layer's input, not its result.
func incomingToRootLayer(history *SetupLayerHistory) string {
	if history == nil {
		return ""
	}
	for i := len(history.Layers) - 2; i >= 0; i-- {
		if c := history.Layers[i].GeneratedContent; c != nil {
			return *c
		}
	}
	if history.OriginalContent != nil {
		return *history.OriginalContent
	}
	return ""
}

// VerifyChainHashes checks every setup entry whose root (topmost) layer declares
// expectChainHash against the XXH3-128 hash of the content entering that layer.
// Only the root layer is consulted; intermediate layers are ignored. The result
// is sorted by filename for deterministic reporting and is empty when all pins
// hold or none are declared.
func VerifyChainHashes(layerMap SetupLayerMap) []ChainHashMismatch {
	names := make([]string, 0, len(layerMap))
	for name := range layerMap {
		names = append(names, name)
	}
	sort.Strings(names)

	var mismatches []ChainHashMismatch
	for _, name := range names {
		history := layerMap[name]
		if history == nil {
			continue
		}
		pin := strings.TrimSpace(history.FinalConfig.ExpectChainHash)
		if pin == "" {
			continue
		}

		expectedHex := strings.ToLower(strings.TrimPrefix(pin, chainHashPrefix))
		incoming := incomingToRootLayer(history)
		actualHex := hashutil.XXH3Hex([]byte(incoming))
		if expectedHex == actualHex {
			continue
		}

		mismatches = append(mismatches, ChainHashMismatch{
			FileName: name,
			Expected: chainHashPrefix + expectedHex,
			Actual:   chainHashPrefix + actualHex,
			Incoming: incoming,
		})
	}
	return mismatches
}
