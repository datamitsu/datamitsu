package inspector

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/datamitsu/datamitsu/internal/inspectortheme"
)

//go:embed inspector.html
var page string

//go:embed protocol.json
var protocolJSON []byte

//go:embed theme.json
var defaultThemeJSON []byte

var protocol = func() struct {
	Placeholder      string `json:"placeholder"`
	SchemaVersion    int    `json:"schemaVersion"`
	ThemePlaceholder string `json:"themePlaceholder"`
} {
	var p struct {
		Placeholder      string `json:"placeholder"`
		SchemaVersion    int    `json:"schemaVersion"`
		ThemePlaceholder string `json:"themePlaceholder"`
	}
	if err := json.Unmarshal(protocolJSON, &p); err != nil {
		panic(err)
	}
	return p
}()

// DefaultTheme is the built-in palette, read through the same strict parser a
// user's file goes through, so what ships cannot drift from what is accepted.
func DefaultTheme() (inspectortheme.Theme, error) {
	theme, err := inspectortheme.Parse(defaultThemeJSON)
	if err != nil {
		return nil, fmt.Errorf("read the built-in inspector theme: %w", err)
	}
	return theme, nil
}

// Render inserts JSON into an inert script element. Marshal's HTML escaping is
// required: a config string containing </script> must never terminate that element.
// The theme is baked in as custom properties, so an exported file carries its
// colors wherever it is hosted.
func Render(manifest Manifest, theme inspectortheme.Theme) ([]byte, error) {
	if strings.Count(page, protocol.Placeholder) != 1 {
		return nil, errors.New("inspector template must contain exactly one manifest placeholder; rebuild the inspector")
	}
	if strings.Count(page, protocol.ThemePlaceholder) != 1 {
		return nil, errors.New("inspector template must contain exactly one theme placeholder; rebuild the inspector")
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode inspector manifest: %w", err)
	}
	css := inspectortheme.CSS(theme)
	if strings.Contains(css, "</style") || strings.Contains(css, "<") {
		return nil, errors.New("theme colors must not contain markup")
	}
	rendered := strings.Replace(page, protocol.Placeholder, string(data), 1)
	return []byte(strings.Replace(rendered, protocol.ThemePlaceholder, css, 1)), nil
}
