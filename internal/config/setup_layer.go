package config

import "slices"

// SetupLayerEntry represents one layer's contribution to a setup entry.
type SetupLayerEntry struct {
	LayerName        string
	GeneratedContent *string
}

// SetupLayerHistory tracks the evolution of a single setup entry across config layers.
type SetupLayerHistory struct {
	FileName        string
	OriginalContent *string // original disk content, read once during first evaluation
	Layers          []SetupLayerEntry
	FinalConfig     ConfigSetup
}

// SetupLayerMap maps filename to layer history.
type SetupLayerMap map[string]*SetupLayerHistory

// GetLastGeneratedContent returns the content from the last layer that produced content,
// walking backward through the layer list. Returns nil if no layer generated content.
func GetLastGeneratedContent(history *SetupLayerHistory) *string {
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
