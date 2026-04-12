package config

// InitLayerEntry represents one layer's contribution to an Init entry.
type InitLayerEntry struct {
	LayerName        string
	GeneratedContent *string
}

// InitLayerHistory tracks the evolution of a single Init entry across config layers.
type InitLayerHistory struct {
	FileName        string
	OriginalContent *string // original disk content, read once during first evaluation
	Layers          []InitLayerEntry
	FinalConfig     ConfigInit
}

// InitLayerMap maps filename to layer history.
type InitLayerMap map[string]*InitLayerHistory

// GetLastGeneratedContent returns the content from the last layer that produced content,
// walking backward through the layer list. Returns nil if no layer generated content.
func GetLastGeneratedContent(history *InitLayerHistory) *string {
	if history == nil {
		return nil
	}
	for i := len(history.Layers) - 1; i >= 0; i-- {
		layer := &history.Layers[i]
		if layer.GeneratedContent != nil {
			return layer.GeneratedContent
		}
	}
	return nil
}
