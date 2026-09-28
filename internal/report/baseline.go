package report

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"
)

// BaselineSchema names the shape of a baseline document.
const BaselineSchema = "datamitsu.baseline/1"

// Baseline is the fingerprints of the findings a run reported: a later run
// given it with --baseline gates only on findings it does not hold. It is made
// on purpose from a run's own report, never automatically.
type Baseline struct {
	Schema string `json:"schema"`
	// Fingerprint is the version of the fingerprints it holds.
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt"`
	// Datamitsu is the build and configuration of the run it was made from.
	Datamitsu Producer       `json:"datamitsu"`
	Source    BaselineSource `json:"source"`
	// Fingerprints are sorted and unique.
	Fingerprints []string `json:"fingerprints"`
}

// BaselineSource is the run a baseline was made from.
type BaselineSource struct {
	StartedAt time.Time  `json:"startedAt"`
	CI        BaselineCI `json:"ci"`
	Complete  bool       `json:"complete"`
	// Incomplete are the reasons of the run and of its tools: a baseline of an
	// incomplete run holds fewer fingerprints, which only suppresses fewer
	// findings.
	Incomplete []Reason `json:"incomplete"`
}

// BaselineCI is the commit and ref of the run a baseline was made from.
type BaselineCI struct {
	SHA string `json:"sha"`
	Ref string `json:"ref"`
}

// BaselineSet is the fingerprints a run is matched against.
type BaselineSet map[string]bool

// NewBaseline is the baseline of run: the fingerprint of every finding it
// reported in any operation, whatever its level. A synthetic finding stands
// for a failure, not a finding, and is not held.
func NewBaseline(run *Run, createdAt time.Time) Baseline {
	b := Baseline{
		Schema:      BaselineSchema,
		Fingerprint: FingerprintVersion,
		CreatedAt:   createdAt.UTC(),
		Datamitsu:   run.Datamitsu,
		Source: BaselineSource{
			StartedAt:  run.StartedAt,
			CI:         BaselineCI{SHA: run.CI.SHA, Ref: run.CI.Ref},
			Complete:   run.Complete,
			Incomplete: IncompleteReasons(run),
		},
		Fingerprints: []string{},
	}
	for fp := range fingerprintsOf(run) {
		b.Fingerprints = append(b.Fingerprints, fp)
	}
	sort.Strings(b.Fingerprints)
	return b
}

// IncompleteReasons is the union of the reasons of run and of its tools.
func IncompleteReasons(run *Run) []Reason {
	set := map[Reason]bool{}
	for _, r := range run.Incomplete {
		set[r] = true
	}
	for _, op := range run.Operations {
		for _, tr := range op.Tools {
			for _, r := range tr.Incomplete {
				set[r] = true
			}
		}
	}
	return sortedReasons(set)
}

func fingerprintsOf(run *Run) BaselineSet {
	set := BaselineSet{}
	for _, op := range run.Operations {
		for _, tr := range op.Tools {
			for _, inv := range tr.Invocations {
				for _, f := range inv.Findings {
					if f.Kind != kindSynthetic && f.Fingerprint != "" {
						set[f.Fingerprint] = true
					}
				}
			}
		}
	}
	return set
}

// ErrBaseline marks a file LoadBaseline could not take as a baseline.
var ErrBaseline = errors.New("not a baseline")

// LoadBaseline reads the fingerprints a run is matched against from path: a
// baseline document, or a run's own report, whose findings are taken as one.
// It returns the version of the fingerprints; a document of another schema,
// or of fingerprints of another version, is refused (ErrBaseline).
func LoadBaseline(path string) (BaselineSet, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read baseline: %w", err)
	}
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, "", fmt.Errorf("%w: %s is not JSON: %w", ErrBaseline, path, err)
	}
	switch head.Schema {
	case BaselineSchema:
		var b Baseline
		if err := json.Unmarshal(data, &b); err != nil {
			return nil, "", fmt.Errorf("%w: %s: %w", ErrBaseline, path, err)
		}
		if b.Fingerprint != FingerprintVersion {
			return nil, b.Fingerprint, fmt.Errorf("%w: %s holds fingerprints of version %q, and this build computes %q: "+
				"make it again with `datamitsu report baseline`", ErrBaseline, path, b.Fingerprint, FingerprintVersion)
		}
		set := make(BaselineSet, len(b.Fingerprints))
		for _, fp := range b.Fingerprints {
			if !isFingerprint(fp) {
				return nil, b.Fingerprint, fmt.Errorf("%w: %s holds %q, which is not a fingerprint", ErrBaseline, path, fp)
			}
			set[fp] = true
		}
		return set, b.Fingerprint, nil
	case SchemaVersion:
		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			return nil, "", fmt.Errorf("%w: %s: %w", ErrBaseline, path, err)
		}
		return fingerprintsOf(&run), FingerprintVersion, nil
	}
	return nil, "", fmt.Errorf("%w: %s has schema %q (want %s, or a report, %s)",
		ErrBaseline, path, head.Schema, BaselineSchema, SchemaVersion)
}

// isFingerprint reports 64 lowercase hexadecimal characters.
func isFingerprint(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
