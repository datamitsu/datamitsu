package dockerfile

import (
	"encoding/json"
	"fmt"
	"path"

	"github.com/datamitsu/datamitsu/internal/config"
)

// OCIMapEntry describes one final-stage COPY --link layer. Subtree is the
// COPY root (e.g. ".bin/golangci-lint"); the com.datamitsu.subtree annotation
// the post-process writes is hash-level (".bin/golangci-lint/<hash>"), so the
// script appends the hash directory it discovers by listing the layer tar —
// and must verify that directory is the layer's single top-level entry under
// Subtree. ".uv/python" is already exact (no hash segment).
type OCIMapEntry struct {
	Subtree string `json:"subtree"`
	Kind    string `json:"kind"` // binary|runtime|app|uv-python
	App     string `json:"app,omitempty"`
}

// OCIMap is the --emit-oci-map output consumed by the bundle post-process
// script. The entries mirror the final stage's per-subtree COPY --link
// instructions in emission order; since those are the LAST instructions of
// the final stage, they correspond to the last len(Layers) layers of the
// built image manifest (a count/path drift is caught by the consumer's
// write-allowlist at pull time, and should additionally be checked by the
// post-process script).
type OCIMap struct {
	Version   int           `json:"version"`
	StoreRoot string        `json:"storeRoot"` // absolute in-image store root (com.datamitsu.store-root)
	Layers    []OCIMapEntry `json:"layers"`
}

// BuildOCIMap derives the layer→subtree mapping from the same plan traversal
// writeFinalStage renders, so the two cannot drift.
func BuildOCIMap(plan Plan, opts RenderOptions) OCIMap {
	m := OCIMap{
		Version:   1,
		StoreRoot: path.Join(opts.StoreRoot, "store"),
	}

	for _, rt := range plan.RuntimeStages {
		if !runtimeCopiedToFinal(rt.Kind) {
			continue
		}
		m.Layers = append(m.Layers, OCIMapEntry{
			Subtree: runtimeSubtree(rt.Name),
			Kind:    "runtime",
			App:     rt.Name,
		})
	}

	for _, ts := range plan.RuntimeAppStages {
		m.Layers = append(m.Layers, OCIMapEntry{
			Subtree: appEnvSubtree(ts.Kind, ts.App),
			Kind:    "app",
			App:     ts.App,
		})
		if ts.Kind == config.RuntimeKindUV {
			m.Layers = append(m.Layers, OCIMapEntry{
				Subtree: uvPythonSubtree,
				Kind:    "uv-python",
			})
		}
	}

	for _, bs := range plan.BinaryStages {
		m.Layers = append(m.Layers, OCIMapEntry{
			Subtree: binaryAppSubtree(bs.App),
			Kind:    "binary",
			App:     bs.App,
		})
	}

	return m
}

// MarshalOCIMap renders the map as stable, indented JSON with a trailing
// newline, matching the determinism contract of Render.
func MarshalOCIMap(m OCIMap) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal oci map: %w", err)
	}
	return append(data, '\n'), nil
}
