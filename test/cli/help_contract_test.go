package cli_test

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/datamitsu/datamitsu/internal/clitest"
)

type helpContract struct {
	Path        []string `json:"path"`
	Usage       string   `json:"usage"`
	Commands    []string `json:"commands"`
	Flags       []string `json:"flags"`
	GlobalFlags []string `json:"globalFlags"`
}

var (
	helpColumns         = regexp.MustCompile(`\s{2,}`)
	helpFlagDeclaration = regexp.MustCompile(`^(?:(-\w), )?--([\w-]+)(\[=true\|false\])?(?: (\S+))?$`)
	helpDefault         = regexp.MustCompile(`\(default (".*"|true|false|-?\d+(?:\.\d+)?|\[[^\]]*\])\)$`)
)

func TestHelpSemanticContracts(t *testing.T) {
	data, err := os.ReadFile("testdata/help-contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var contracts []helpContract
	if err := json.Unmarshal(data, &contracts); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for _, want := range contracts {
		name := strings.Join(want.Path, " ")
		if seen[name] {
			t.Fatalf("duplicate help contract for %q", name)
		}
		seen[name] = true
		if name == "" {
			name = "root"
		}
		t.Run(name, func(t *testing.T) {
			for _, flag := range []string{"--help", "-h"} {
				t.Run(flag, func(t *testing.T) {
					args := append(slices.Clone(want.Path), flag)
					assertHelpContract(t, want, args)
				})
			}
			if slices.Equal(want.Path, []string{"version"}) {
				t.Run("help subcommand", func(t *testing.T) {
					assertHelpContract(t, want, []string{"help", "version"})
				})
			}
		})
	}
}

func assertHelpContract(t *testing.T, want helpContract, args []string) {
	t.Helper()
	res := clitest.Run(t, clitest.RunOptions{}, args...)
	if res.ExitCode != 0 || res.Stderr != "" {
		t.Fatalf("%v: exit/stderr = %d/%q, want 0/empty", args, res.ExitCode, res.Stderr)
	}
	got := parseHelpContract(t, res.Stdout)
	got.Path = slices.Clone(want.Path)
	for _, fields := range [][]string{got.Commands, got.Flags, got.GlobalFlags, want.Commands, want.Flags, want.GlobalFlags} {
		slices.Sort(fields)
	}
	if !reflect.DeepEqual(got, want) {
		actual, _ := json.MarshalIndent(got, "", "  ")
		expected, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("help contract changed\ngot:\n%s\nwant:\n%s", actual, expected)
	}
}

func parseHelpContract(t *testing.T, text string) helpContract {
	t.Helper()
	contract := helpContract{Commands: []string{}, Flags: []string{}, GlobalFlags: []string{}}
	section := ""
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		switch line {
		case "Usage:", "Available Commands:", "Flags:", "Global Flags:":
			section = line
			continue
		case "":
			section = ""
			continue
		}
		switch section {
		case "Usage:":
			contract.Usage = line
			section = ""
		case "Available Commands:":
			contract.Commands = append(contract.Commands, strings.Fields(line)[0])
		case "Flags:", "Global Flags:":
			columns := helpColumns.Split(line, 2)
			if len(columns) != 2 {
				t.Fatalf("cannot parse flag metadata from %q", line)
			}
			declaration := helpFlagDeclaration.FindStringSubmatch(columns[0])
			if declaration == nil {
				t.Fatalf("cannot parse flag declaration %q", columns[0])
			}
			kind := declaration[4]
			if kind == "" {
				kind = "bool"
			}
			flag := "--" + declaration[2] + " " + kind
			if declaration[1] != "" {
				flag = declaration[1] + ", " + flag
			}
			if value := helpDefault.FindStringSubmatch(columns[1]); value != nil {
				flag += " (default " + value[1] + ")"
			} else if kind == "bool" {
				flag += " (default false)"
			}
			if section == "Flags:" {
				contract.Flags = append(contract.Flags, flag)
			} else {
				contract.GlobalFlags = append(contract.GlobalFlags, flag)
			}
		}
	}
	return contract
}
