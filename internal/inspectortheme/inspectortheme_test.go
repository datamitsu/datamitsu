package inspectortheme

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseNamesTheKeyThatIsWrong(t *testing.T) {
	for _, tc := range []struct {
		file string
		name string
		want string
	}{
		{file: `{"dark":{"runtme":{"node":"#fff"}}}`, name: "group typo", want: "dark.runtme: unknown key"},
		{file: `{"dark":{"runtime":{"nod":"#fff"}}}`, name: "token typo", want: "dark.runtime.nod: unknown key"},
		{file: `{"darkk":{}}`, name: "mode typo", want: "darkk: unknown key"},
		{file: `{"dark":{"runtime":{"node":"blue"}}}`, name: "named color", want: `"blue" is not a color`},
		{file: `{"dark":{"runtime":{"node":"#ff"}}}`, name: "short color", want: `"#ff" is not a color`},
		{file: `{"dark":[]}`, name: "wrong shape", want: "dark: expected an object of groups"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.file))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse(%s) = %v, want error containing %q", tc.file, err, tc.want)
			}
		})
	}
	for _, accepted := range []string{`{}`, `{"$schema":"https://example.test/s.json"}`, `{"light":{"accent":{"base":"#abc"}}}`, `{"dark":{"surface":{"scrim":"#00000088"}}}`} {
		if _, err := Parse([]byte(accepted)); err != nil {
			t.Fatalf("Parse(%s) = %v, want nil", accepted, err)
		}
	}
}

func TestMergeReplacesOneTokenPerMode(t *testing.T) {
	base := Theme{
		"dark":  {"surface": {"base": "#000000", "panel": "#111111"}, "text": {"base": "#ffffff"}},
		"light": {"surface": {"base": "#ffffff", "panel": "#eeeeee"}, "text": {"base": "#000000"}},
	}
	override, err := Parse([]byte(`{"dark":{"surface":{"base":"#151009"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	merged := Merge(base, override)
	if merged["dark"]["surface"]["base"] != "#151009" {
		t.Fatalf("override did not apply: %v", merged["dark"]["surface"])
	}
	if merged["dark"]["surface"]["panel"] != "#111111" || merged["dark"]["text"]["base"] != "#ffffff" {
		t.Fatalf("unmentioned tokens were lost: %v", merged["dark"])
	}
	if merged["light"]["surface"]["base"] != "#ffffff" {
		t.Fatal("a dark override reached the light mode")
	}
	if base["dark"]["surface"]["base"] != "#000000" {
		t.Fatal("merge mutated the base theme")
	}
	empty, err := Parse([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if Merge(base, empty)["dark"]["surface"]["base"] != "#000000" {
		t.Fatal("an empty file changed the theme")
	}
}

func TestCSSCarriesEveryTokenUnderItsMode(t *testing.T) {
	theme := Theme{
		"dark":  {"surface": {"base": "#151009"}},
		"light": {"surface": {"base": "#ebe6dc"}},
	}
	css := CSS(theme)
	light, dark, ok := strings.Cut(css, `:root[data-theme="dark"]`)
	if !ok {
		t.Fatalf("no dark block: %s", css)
	}
	if !strings.Contains(light, "--surface-base:#ebe6dc;") || !strings.Contains(dark, "--surface-base:#151009;") {
		t.Fatalf("modes did not land in their own block: %s", css)
	}
}

func TestContrastWarnsOnlyBelowAA(t *testing.T) {
	readable := Theme{
		"dark":  {"surface": {"base": "#151009"}, "text": {"base": "#f5efe3", "muted": "#c9b99a"}, "accent": {"base": "#d4a855"}},
		"light": {"surface": {"base": "#ebe6dc"}, "text": {"base": "#5a4029", "muted": "#6b543f"}, "accent": {"base": "#7b5e28"}},
	}
	if warnings := ContrastWarnings(readable); len(warnings) > 0 {
		t.Fatalf("readable theme warned: %v", warnings)
	}
	faint := Merge(readable, Theme{"light": {"text": {"muted": "#d8d2c6"}}})
	warnings := ContrastWarnings(faint)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "light: text.muted on surface.base") {
		t.Fatalf("expected one warning about the faint pair, got %v", warnings)
	}
}

func TestSchemaDescribesTheSameTokens(t *testing.T) {
	data, err := Schema("https://example.test/inspector-theme.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Properties map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	for _, mode := range Modes {
		groups := schema.Properties[mode].Properties
		if len(groups) != len(Groups) {
			t.Fatalf("%s describes %d groups, the parser knows %d", mode, len(groups), len(Groups))
		}
		for group, tokens := range Groups {
			if len(groups[group].Properties) != len(tokens) {
				t.Fatalf("%s.%s describes %d tokens, the parser knows %d", mode, group, len(groups[group].Properties), len(tokens))
			}
		}
	}
}
