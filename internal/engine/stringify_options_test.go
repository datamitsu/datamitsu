package engine

import "testing"

func TestStringifyOptionsRoundTrip(t *testing.T) {
	for _, tc := range []struct{ name, format, input, options, output, check string }{
		{"YAML flow multiline", "YAML", `{x: "a\nb"}`, `{flow:true}`, "{x: \"a\\nb\"}\n", `parsed.x === input.x`},
		{"YAML flow multiline key", "YAML", `{["a\nb"]:["c\nd"]}`, `{flow:true}`, "{\"a\\nb\": [\"c\\nd\"]}\n", `parsed["a\nb"][0] === input["a\nb"][0]`},
		{"YAML indent", "YAML", `{x:{label:"# tag"}}`, `{indent:4, quoteStyle:"single"}`, "x:\n    label: '# tag'\n", `parsed.x.label === input.x.label`},
		{"TOML basic quote", "TOML", `{table:{text:"a\\b"}, title:"root"}`, `{quoteStyle:"double"}`, "title = \"root\"\n[table]\ntext = \"a\\\\b\"\n", `parsed.title === input.title && parsed.table.text === input.table.text`},
		{"INI delimiter", "INI", `[{name:"section",properties:{key:"left=right"}}]`, `{align:false,delimiter:":",indent:"\t",spaceAroundDelimiter:false,lineEnding:"crlf"}`, "[section]\r\n\tkey:left=right\r\n", `INI.toRecord(parsed).section.key === input[0].properties.key`},
		{"JSON tab", "JSON", `{key:[1]}`, `{indent:"\t"}`, "{\n\t\"key\": [\n\t\t1\n\t]\n}", `parsed.key[0] === input.key[0]`},
		{"Ignore order and CRLF", "tools.Ignore", `{A:["a"],B:["!b"]}`, `{groupOrder:["B"],blankLinesBetweenGroups:false,lineEnding:"crlf"}`, "# B\r\n!b\r\n# A\r\na", `parsed.groupOrder.join(",") === "B,A" && parsed.groups.A[0] === input.A[0] && parsed.groups.B[0] === input.B[0]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine(t)
			if err := e.vm.Set("expected", tc.output); err != nil {
				t.Fatal(err)
			}
			_, err := e.vm.RunString(`const input = ` + tc.input + `; const output = ` + tc.format + `.stringify(input,` + tc.options + `);
			if (output !== expected) throw Error("rendered " + JSON.stringify(output) + ", expected " + JSON.stringify(expected));
			const parsed = ` + tc.format + `.parse(output);
			if (!(` + tc.check + `)) throw Error("round trip: " + JSON.stringify(parsed));`)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
