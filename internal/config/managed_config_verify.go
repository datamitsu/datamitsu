package config

import (
	"sort"
	"strings"

	"github.com/datamitsu/datamitsu/internal/digest"
)

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
func incomingToRootLayer(history *ManagedConfigLayerHistory) string {
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

// ChainHashEntry is a managed config file paired with the XXH3-128 hash of the content
// entering its root (topmost) layer — the value an expectChainHash pin must match.
type ChainHashEntry struct {
	FileName string
	Hash     string // "xxh3:<hex>"
}

// ChainHashes returns, for every managed config file in the layer map, the chain hash that
// VerifyChainHashes compares an expectChainHash pin against (the content entering
// the file's root layer), sorted by filename. It is the introspection counterpart
// of the gate: the printed value can be copied straight into a pin.
func ChainHashes(layerMap ManagedConfigLayerMap) []ChainHashEntry {
	names := make([]string, 0, len(layerMap))
	for name := range layerMap {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]ChainHashEntry, 0, len(names))
	for _, name := range names {
		history := layerMap[name]
		if history == nil {
			continue
		}
		out = append(out, ChainHashEntry{
			FileName: name,
			Hash:     digest.XXH3Of([]byte(incomingToRootLayer(history))).String(),
		})
	}
	return out
}

// VerifyChainHashes checks every managed config entry whose root (topmost) layer declares
// expectChainHash against the XXH3-128 hash of the content entering that layer.
// Only the root layer is consulted; intermediate layers are ignored. The result
// is sorted by filename for deterministic reporting and is empty when all pins
// hold or none are declared.
func VerifyChainHashes(layerMap ManagedConfigLayerMap) []ChainHashMismatch {
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
		// A pin protects repository overrides from upstream drift. An internal
		// file carries no overrides, and the pin was taken over the repository
		// render, which an internal render does not equal.
		if pin == "" || history.FinalConfig.Placement == PlacementInternal {
			continue
		}

		// The pin is canonical after load validation. A pin that does not parse
		// is itself a mismatch — the gate fails closed, never open: manufacturing
		// a match from an unparsable pin would silence exactly the drift the
		// pin exists to catch. (--no-verify-hash skips this whole function, so
		// no escape hatch needs loose parsing here.)
		expected, perr := digest.ParseXXH3(pin)
		incoming := incomingToRootLayer(history)
		actual := digest.XXH3Of([]byte(incoming))
		if perr == nil && expected.Hex() == actual.Hex() {
			continue
		}

		expectedStr := pin
		if perr == nil {
			expectedStr = expected.String()
		}
		mismatches = append(mismatches, ChainHashMismatch{
			FileName: name,
			Expected: expectedStr,
			Actual:   actual.String(),
			Incoming: incoming,
		})
	}
	return mismatches
}
