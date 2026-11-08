package runtimemanager

import (
	"encoding/json"
	"errors"
	"fmt"
)

// MarshalJSON encodes the non-serializable error as its message string.
func (result RuntimeInstallResult) MarshalJSON() ([]byte, error) {
	var message string
	if result.Error != nil {
		message = result.Error.Error()
	}
	data, err := json.Marshal(struct {
		Name  string `json:"name"`
		Error string `json:"error,omitempty"`
	}{Name: result.Name, Error: message})
	if err != nil {
		return nil, fmt.Errorf("encode runtime install result: %w", err)
	}
	return data, nil
}

// UnmarshalJSON restores an error message as an error value.
func (result *RuntimeInstallResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decode runtime install result: %w", err)
	}
	result.Name = wire.Name
	if wire.Error != "" {
		result.Error = errors.New(wire.Error)
	} else {
		result.Error = nil
	}
	return nil
}
