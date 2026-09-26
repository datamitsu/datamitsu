package jsonsort

import (
	"strings"
	"testing"
)

type entry struct {
	URL         string            `json:"url"`
	Hash        string            `json:"hash"`
	ContentType string            `json:"contentType"`
	BinaryPath  *string           `json:"binaryPath,omitempty"`
	ExtractDir  bool              `json:"extractDir,omitempty"`
	Tags        []string          `json:"tags"`
	Nested      map[string]*entry `json:"nested,omitempty"`
	Size        int64             `json:"size"`
	Ratio       float64           `json:"ratio"`
}

func TestMarshalIndent(t *testing.T) {
	path := "bin/tool"
	value := map[string]any{
		"zeta": entry{
			URL: "https://example.test/tool.tar.gz", Hash: "abc", ContentType: "tar.gz", BinaryPath: &path,
			Tags: []string{"z", "a", "m"}, Size: 9007199254740993, Ratio: 0.1,
			Nested: map[string]*entry{"b": {URL: "u", Hash: "h", ContentType: "binary"}, "a": {URL: "u2", Hash: "h2", ContentType: "zip"}},
		},
		"alpha": 1,
	}

	out, err := MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	want := `{
  "alpha": 1,
  "zeta": {
    "binaryPath": "bin/tool",
    "contentType": "tar.gz",
    "hash": "abc",
    "nested": {
      "a": {
        "contentType": "zip",
        "hash": "h2",
        "ratio": 0,
        "size": 0,
        "tags": null,
        "url": "u2"
      },
      "b": {
        "contentType": "binary",
        "hash": "h",
        "ratio": 0,
        "size": 0,
        "tags": null,
        "url": "u"
      }
    },
    "ratio": 0.1,
    "size": 9007199254740993,
    "tags": [
      "z",
      "a",
      "m"
    ],
    "url": "https://example.test/tool.tar.gz"
  }
}`
	if got != want {
		t.Errorf("MarshalIndent() =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "extractDir") {
		t.Error("an omitted field was written")
	}
}

func TestMarshalIndent_Error(t *testing.T) {
	if _, err := MarshalIndent(make(chan int), "", "  "); err == nil {
		t.Error("MarshalIndent(chan) = nil error, want one")
	}
}
