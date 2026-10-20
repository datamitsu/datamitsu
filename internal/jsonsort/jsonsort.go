// Package jsonsort marshals a value as JSON with every object's keys in
// alphabetical order, whatever the order of the struct fields behind it. The
// files the devtools pull commands write are read by people and diffed in
// pull requests, so their shape must not depend on how a Go struct happens
// to be declared.
package jsonsort

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MarshalIndent encodes v like json.MarshalIndent, with the keys of every
// object sorted. Arrays keep their order and numbers their spelling.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding JSON: %w", err)
	}

	// A round trip through untyped values: encoding/json writes the keys of a
	// map in sorted order, and json.Number keeps a number as it was written.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, fmt.Errorf("re-reading encoded JSON: %w", err)
	}

	out, err := json.MarshalIndent(generic, prefix, indent)
	if err != nil {
		return nil, fmt.Errorf("encoding sorted JSON: %w", err)
	}
	return out, nil
}
