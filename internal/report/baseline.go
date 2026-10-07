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
// reported in any operation, whatever its level, under the run's fingerprint
// version (FingerprintVersion for a document that states none). A synthetic
// finding stands for a failure, not a finding, and is not held.
func NewBaseline(run *Run, createdAt time.Time) Baseline {
	b := Baseline{
		Schema:      BaselineSchema,
		Fingerprint: run.Fingerprint,
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
	if b.Fingerprint == "" {
		b.Fingerprint = FingerprintVersion
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

// LoadedBaseline is a baseline as a run reads it: the fingerprints, their
// version, and how complete the run they come from was.
type LoadedBaseline struct {
	Set        BaselineSet
	Version    string
	Complete   bool
	Incomplete []Reason
	// FromReport is set when the file was a run's own report rather than a
	// baseline document.
	FromReport bool
}

// LoadBaseline reads the fingerprints a run is matched against from path: a
// baseline document, or a run's own report, whose findings are taken as one.
// A document of another schema, or of fingerprints of another version, is
// refused (ErrBaseline).
func LoadBaseline(path string) (LoadedBaseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LoadedBaseline{}, fmt.Errorf("read baseline: %w", err)
	}
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return LoadedBaseline{}, fmt.Errorf("%w: %s is not JSON: %w", ErrBaseline, path, err)
	}
	switch head.Schema {
	case BaselineSchema:
		var b Baseline
		if err := json.Unmarshal(data, &b); err != nil {
			return LoadedBaseline{}, fmt.Errorf("%w: %s: %w", ErrBaseline, path, err)
		}
		if err := sameVersion(path, b.Fingerprint); err != nil {
			return LoadedBaseline{}, err
		}
		set := make(BaselineSet, len(b.Fingerprints))
		for _, fp := range b.Fingerprints {
			if !isFingerprint(fp) {
				return LoadedBaseline{}, fmt.Errorf("%w: %s holds %q, which is not a fingerprint", ErrBaseline, path, fp)
			}
			set[fp] = true
		}
		return LoadedBaseline{Set: set, Version: b.Fingerprint, Complete: b.Source.Complete, Incomplete: b.Source.Incomplete}, nil
	case SchemaVersion:
		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			return LoadedBaseline{}, fmt.Errorf("%w: %s: %w", ErrBaseline, path, err)
		}
		Revise(&run)
		if err := sameVersion(path, run.Fingerprint); err != nil {
			return LoadedBaseline{}, err
		}
		return LoadedBaseline{
			Set: fingerprintsOf(&run), Version: run.Fingerprint, Complete: run.Complete,
			Incomplete: IncompleteReasons(&run), FromReport: true,
		}, nil
	}
	return LoadedBaseline{}, fmt.Errorf("%w: %s has schema %q (want %s, or a report, %s)",
		ErrBaseline, path, head.Schema, BaselineSchema, SchemaVersion)
}

func sameVersion(path, version string) error {
	if version == FingerprintVersion {
		return nil
	}
	return fmt.Errorf("%w: %s holds fingerprints of version %q, and this build computes %q: "+
		"make it again from a run of this build", ErrBaseline, path, version, FingerprintVersion)
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
