package report

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/datamitsu/datamitsu/internal/textpos"
)

func TestSecretValues(t *testing.T) {
	environ := []string{
		"GITHUB_TOKEN=value-of-the-token",
		"npm_token=abcdefgh",
		"API_KEY=long-enough-1",
		"KEYBOARD=layout-name",
		"DB_PASSWORD=short",
		"MY_CREDENTIALS_FILE=/run/secrets/creds",
		"PATH=/usr/bin:/bin",
		"SECRET_EMPTY=",
		"NOT_A_PAIR",
	}
	got := SecretValues(environ, map[string]string{"CLIENT_SECRET": "s3cr3t-value"}, nil)
	want := []string{"/run/secrets/creds", "value-of-the-token", "long-enough-1", "s3cr3t-value", "abcdefgh"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SecretValues() = %q, want %q (longest first)", got, want)
	}
}

// Every string the document holds is masked, wherever it sits; what is not a
// string is left alone.
func TestMask(t *testing.T) {
	const secret = "abcdefgh12"
	code := 3
	stamp := time.Date(2023, 11, 14, 22, 13, 20, 0, time.UTC)
	run := &Run{
		Schema:    SchemaVersion,
		StartedAt: stamp,
		Selection: Selection{Mode: "paths", Paths: []string{"dir-" + secret + "/a"}},
		Operations: []Operation{{
			Name: "lint",
			Tools: []ToolRun{{
				Name: "leaky",
				Invocations: []Invocation{{
					ExitCode:   &code,
					OutputTail: "token=" + secret + "\n",
					Findings: []Finding{{
						Message:  "found " + secret + " twice: " + secret,
						Location: Location{Path: "a", Chars: &textpos.Span{Start: 1, End: 2}},
					}},
				}},
			}},
		}},
		Exports: []Export{{Format: "json", Path: "out.json", Detail: secret}},
	}
	Mask(run, []string{secret, "abcdefgh"})

	data, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	if strings.Contains(doc, "abcdefgh") {
		t.Errorf("the document still holds the secret:\n%s", doc)
	}
	inv := run.Operations[0].Tools[0].Invocations[0]
	if inv.Findings[0].Message != "found *** twice: ***" || inv.OutputTail != "token=***\n" ||
		run.Selection.Paths[0] != "dir-***/a" || run.Exports[0].Detail != "***" {
		t.Errorf("masked = %q, %q, %q, %q", inv.Findings[0].Message, inv.OutputTail, run.Selection.Paths[0], run.Exports[0].Detail)
	}
	if *inv.ExitCode != 3 || !run.StartedAt.Equal(stamp) || *inv.Findings[0].Location.Chars != (textpos.Span{Start: 1, End: 2}) {
		t.Error("masking changed a value that is not a string")
	}
}

// A secret JSON escapes — a quote, a backslash — is masked in its escaped
// spelling too, the one a routed log line's fields carry.
func TestMaskJSONSpelling(t *testing.T) {
	const secret = `token"with\\slash`
	run := &Run{Operations: []Operation{{Name: `{"files":["dir/token\"with\\\\slash/a"]}` + " and " + secret}}}
	Mask(run, []string{secret})
	if got := run.Operations[0].Name; got != `{"files":["dir/***/a"]}`+" and "+Masked {
		t.Errorf("masked = %s", got)
	}
}

func TestMaskNothing(t *testing.T) {
	run := &Run{Schema: SchemaVersion}
	Mask(run, nil)
	Mask(nil, []string{"abcdefgh"})
	if run.Schema != SchemaVersion {
		t.Error("masking with no secret changed the document")
	}
}

func TestMaskMapValues(t *testing.T) {
	type holder struct {
		Options map[string]string
	}
	h := holder{Options: map[string]string{"k": "x-abcdefgh"}}
	maskValue(reflect.ValueOf(&h).Elem(), strings.NewReplacer("abcdefgh", Masked))
	if h.Options["k"] != "x-***" {
		t.Errorf("map value = %q, want it masked", h.Options["k"])
	}
}
