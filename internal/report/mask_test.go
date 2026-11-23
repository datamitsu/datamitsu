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
		Fingerprint:     "",
		Datamitsu:       Producer{Version: "", Configuration: ""},
		EndedAt:         time.Time{},
		FailFast:        false,
		Complete:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},

		StartedAt: stamp,
		Selection: Selection{
			Dir:           "",
			Tools:         nil,
			FileScoped:    false,
			ExcludedTools: nil,
			Mode:          "paths", Paths: []string{"dir-" + secret + "/a"},
		},
		Operations: []Operation{{
			Ran:             false,
			Success:         false,
			Duration:        0,
			Skipped:         nil,
			Cancelled:       nil,
			ChangesObserved: false,
			ChangesReason:   "",
			ChangesDetail:   "",
			ChangesScope:    nil,
			Changes:         nil,

			Name: "lint",
			Tools: []ToolRun{{
				App:             AppRef{Name: "", Kind: "", Version: "", OfficialURL: ""},
				Parser:          nil,
				FailOn:          "",
				GateActive:      false,
				Category:        "",
				Complete:        false,
				Incomplete:      nil,
				RedactedReasons: nil,
				mayHoldSecrets:  false,

				Name: "leaky",
				Invocations: []Invocation{{
					ID:                 "",
					TaskID:             "",
					Dir:                "",
					Scope:              "",
					Granularity:        "",
					Arity:              "",
					Coverage:           "",
					WholeUnit:          false,
					State:              "",
					RedactedState:      0,
					Success:            false,
					FailureKind:        "",
					Duration:           0,
					Extraction:         "",
					RedactedExtraction: 0,
					Provenance:         "",
					Files:              nil,
					Step:               0,
					Changes:            nil,

					ExitCode:   &code,
					OutputTail: "token=" + secret + "\n",
					Findings: []Finding{{
						Fingerprint:      "",
						FingerprintBasis: "",
						Tool:             "",
						Source:           "",
						Code:             "",
						RuleURL:          "",
						Severity:         "",
						Reported:         false,
						Gates:            false,
						Shown:            false,
						Baselined:        false,
						Kind:             "",
						Provenance:       "",
						lineHash:         "",

						Message: "found " + secret + " twice: " + secret,
						Location: Location{
							Row:       0,
							EndRow:    0,
							Col:       0,
							EndCol:    0,
							Unit:      "",
							Bytes:     nil,
							Utf16:     nil,
							Precision: "",
							Path:      "a", Chars: &textpos.Span{Start: 1, End: 2},
						},
					}},
				}},
			}},
		}},
		Exports: []Export{{
			Status:    "",
			Companion: "",
			Omitted:   nil,
			Format:    "json", Path: "out.json", Detail: secret,
		}},
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
	run := &Run{
		Fingerprint:     "",
		Datamitsu:       Producer{Version: "", Configuration: ""},
		StartedAt:       time.Time{},
		EndedAt:         time.Time{},
		Selection:       Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil},
		FailFast:        false,
		Complete:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		Exports:         nil,
		CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Operations: []Operation{{
			Ran:             false,
			Success:         false,
			Duration:        0,
			Skipped:         nil,
			Cancelled:       nil,
			Tools:           nil,
			ChangesObserved: false,
			ChangesReason:   "",
			ChangesDetail:   "",
			ChangesScope:    nil,
			Changes:         nil,
			Name:            `{"files":["dir/token\"with\\\\slash/a"]}` + " and " + secret,
		}},
	}
	Mask(run, []string{secret})
	if got := run.Operations[0].Name; got != `{"files":["dir/***/a"]}`+" and "+Masked {
		t.Errorf("masked = %s", got)
	}
}

func TestMaskNothing(t *testing.T) {
	run := &Run{
		Datamitsu:       Producer{Version: "", Configuration: ""},
		StartedAt:       time.Time{},
		EndedAt:         time.Time{},
		Selection:       Selection{Mode: "", Dir: "", Paths: nil, Tools: nil, FileScoped: false, ExcludedTools: nil},
		FailFast:        false,
		Complete:        false,
		Incomplete:      nil,
		RedactedReasons: nil,
		Operations:      nil,
		Exports:         nil,
		CI:              CIEnvironment{Vendor: "", SHA: "", Ref: "", BaseRef: "", PRNumber: ""},
		Fingerprint:     FingerprintVersion,
	}
	Mask(run, nil)
	Mask(nil, []string{"abcdefgh"})
	if run.Fingerprint != FingerprintVersion {
		t.Error("masking with no secret changed the document")
	}
}

func TestMaskMapValues(t *testing.T) {
	type holder struct {
		Options map[string]string
	}
	h := holder{Options: map[string]string{"k": "x-abcdefgh"}}
	rewriteValue(reflect.ValueOf(&h).Elem(), strings.NewReplacer("abcdefgh", Masked).Replace)
	if h.Options["k"] != "x-***" {
		t.Errorf("map value = %q, want it masked", h.Options["k"])
	}
}

func TestRewriteAll(t *testing.T) {
	type inner struct{ Text string }
	type outer struct {
		Name  string
		Items []inner
		Ptr   *inner
		skip  string
	}
	v := outer{Name: "a#", Items: []inner{{"b#"}}, Ptr: &inner{"c#"}, skip: "d#"}
	RewriteAll(&v, func(s string) string { return strings.ReplaceAll(s, "#", "!") })
	if v.Name != "a!" || v.Items[0].Text != "b!" || v.Ptr.Text != "c!" || v.skip != "d#" {
		t.Errorf("RewriteAll = %+v, want every exported string rewritten", v)
	}
	RewriteAll(v, strings.ToUpper)
	RewriteAll(&v, nil)
}
