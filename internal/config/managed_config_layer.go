package config

import "slices"

// ManagedConfigLayerEntry represents one layer's contribution to a managed config entry.
type ManagedConfigLayerEntry struct {
	LayerName        string
	GeneratedContent *string
}

// ManagedConfigLayerHistory tracks one managed config entry across config layers.
type ManagedConfigLayerHistory struct {
	FileName        string
	OriginalContent *string // original disk content, read once during first evaluation
	Layers          []ManagedConfigLayerEntry
	FinalConfig     ManagedConfig
}

// ManagedConfigLayerMap maps filename to layer history.
type ManagedConfigLayerMap map[string]*ManagedConfigLayerHistory

// GetLastGeneratedContent returns the content from the last layer that produced content,
// walking backward through the layer list. Returns nil if no layer generated content.
func GetLastGeneratedContent(history *ManagedConfigLayerHistory) *string {
	if history == nil {
		return nil
	}
	for _, v := range slices.Backward(history.Layers) {
		layer := &v
		if layer.GeneratedContent != nil {
			return layer.GeneratedContent
		}
	}
	return nil
}
