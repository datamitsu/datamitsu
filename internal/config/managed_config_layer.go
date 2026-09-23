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
	// PristineContent is an ejectable entry's repository render from scratch —
	// no originalContent — which reconciliation compares a repository file
	// against before deleting it. nil when the entry is not ejectable or no layer
	// rendered anything.
	PristineContent *string
	// RenderFailed records that some layer's content() threw during the eager
	// pass. The pass skips such a layer and carries on, so the last generated
	// content no longer says what reconciliation would write for the file.
	RenderFailed bool
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
