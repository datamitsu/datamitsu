package config

import (
	"testing"
)

func TestGetLastGeneratedContent(t *testing.T) {
	t.Run("returns last content layer", func(t *testing.T) {
		content1 := "first content"
		content2 := "second content"

		history := &InitLayerHistory{
			FileName: ".editorconfig",
			Layers: []InitLayerEntry{
				{LayerName: "default", GeneratedContent: &content1},
				{LayerName: "auto", GeneratedContent: &content2},
			},
		}

		result := GetLastGeneratedContent(history)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if *result != "second content" {
			t.Errorf("expected 'second content', got %q", *result)
		}
	})

	t.Run("skips non-content layers", func(t *testing.T) {
		content1 := "first content"

		history := &InitLayerHistory{
			FileName: ".editorconfig",
			Layers: []InitLayerEntry{
				{LayerName: "default", GeneratedContent: &content1},
				{LayerName: "auto", GeneratedContent: nil},
			},
		}

		result := GetLastGeneratedContent(history)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if *result != "first content" {
			t.Errorf("expected 'first content', got %q", *result)
		}
	})

	t.Run("returns nil for empty layers", func(t *testing.T) {
		history := &InitLayerHistory{
			FileName: ".editorconfig",
			Layers:   nil,
		}

		result := GetLastGeneratedContent(history)
		if result != nil {
			t.Errorf("expected nil, got %q", *result)
		}
	})

	t.Run("returns nil when no content layers", func(t *testing.T) {
		history := &InitLayerHistory{
			FileName: ".editorconfig",
			Layers: []InitLayerEntry{
				{LayerName: "default", GeneratedContent: nil},
				{LayerName: "auto", GeneratedContent: nil},
			},
		}

		result := GetLastGeneratedContent(history)
		if result != nil {
			t.Errorf("expected nil, got %q", *result)
		}
	})

	t.Run("nil history returns nil", func(t *testing.T) {
		result := GetLastGeneratedContent(nil)
		if result != nil {
			t.Errorf("expected nil, got %q", *result)
		}
	})
}
