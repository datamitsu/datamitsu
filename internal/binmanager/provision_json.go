package binmanager

import (
	"encoding/json"
	"errors"
	"fmt"
)

// MarshalJSON preserves diagnostic text that an error interface would otherwise lose.
func (result DownloadResult) MarshalJSON() ([]byte, error) {
	var message string
	if result.Error != nil {
		message = result.Error.Error()
	}
	data, err := json.Marshal(struct {
		Name  string `json:"name"`
		Error string `json:"error,omitempty"`
	}{Name: result.Name, Error: message})
	if err != nil {
		return nil, fmt.Errorf("marshal binary download result: %w", err)
	}
	return data, nil
}

// UnmarshalJSON restores operation failures rather than decoding error text as success.
func (result *DownloadResult) UnmarshalJSON(data []byte) error {
	var wire struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("unmarshal binary download result: %w", err)
	}
	result.Name = wire.Name
	if wire.Error != "" {
		result.Error = errors.New(wire.Error)
	} else {
		result.Error = nil
	}
	return nil
}
