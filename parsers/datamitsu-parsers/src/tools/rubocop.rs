//! rubocop — The Ruby Linter/Formatter that Serves and Protects. Ported from the
//! none-ls diagnostics/rubocop builtin.
//!
//! Unlike the flat JSON tools, rubocop nests its diagnostics under
//! `output.files[0].offenses[]`, and each offense carries its span in a nested
//! `location` object (`start_line`/`start_column`/`last_line`/`last_column`),
//! so this walks the JSON with tinyjson directly. `start_column` is 1-based;
//! `last_column` is the parser gem's 0-based exclusive end, which is the 1-based
//! column of the span's last character, so 1 is added to make it exclusive.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "rubocop",
	description: "The Ruby Linter/Formatter that Serves and Protects.",
	url: "https://rubocop.org/",
	severities: &[
		Level("fatal", severity::ERROR),
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("convention", severity::INFO),
		Level("info", severity::INFO),
		Level("refactor", severity::HINT),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-f", "json", "--force-exclusion", "--stdin", "{file}"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	let mut out = Vec::new();
	// output.files[0].offenses[]
	let offenses = value
		.get::<HashMap<String, JsonValue>>()
		.and_then(|root| root.get("files"))
		.and_then(|f| f.get::<Vec<JsonValue>>())
		.and_then(|files| files.first())
		.and_then(|f| f.get::<HashMap<String, JsonValue>>())
		.and_then(|file| file.get("offenses"))
		.and_then(|o| o.get::<Vec<JsonValue>>());

	if let Some(offenses) = offenses {
		for offense in offenses {
			if let Some(d) = offense_to_diagnostic(offense) {
				out.push(d);
			}
		}
	}
	out
}

fn offense_to_diagnostic(offense: &JsonValue) -> Option<RawDiagnostic> {
	let map = offense.get::<HashMap<String, JsonValue>>()?;
	let message = get_str(map, "message")?;

	let loc = map.get("location").and_then(|l| l.get::<HashMap<String, JsonValue>>());
	let (start_line, start_col, last_line, last_col) = match loc {
		Some(l) => (
			get_u32(l, "start_line"),
			get_u32(l, "start_column"),
			get_u32(l, "last_line"),
			get_u32(l, "last_column"),
		),
		None => (None, None, None, None),
	};

	Some(RawDiagnostic {
		message,
		row: start_line,
		col: start_col,
		end_row: last_line,
		end_col: last_col.and_then(|c| c.checked_add(1)),
		code: get_str(map, "cop_name"),
		severity: get_str(map, "severity").and_then(|s| severity::of(DESCRIPTOR.severities, &s)),
		..RawDiagnostic::default()
	})
}

fn get_str(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn get_u32(map: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_offenses_with_location_and_severity() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 2);

		let first = &out[0];
		assert_eq!(first.message, "Use 2 spaces for indentation.");
		assert_eq!(first.row, Some(3));
		assert_eq!(first.col, Some(1));
		assert_eq!(first.end_row, Some(3));
		assert_eq!(first.end_col, Some(5));
		assert_eq!(first.code.as_deref(), Some("Layout/IndentationWidth"));
		assert_eq!(first.severity, Some(severity::INFO));
	}

	#[test]
	fn multiline_offense_keeps_its_end() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		let second = &out[1];
		assert_eq!(second.row, Some(5));
		assert_eq!(second.col, Some(10));
		assert_eq!(second.end_row, Some(7));
		assert_eq!(second.end_col, Some(3));
		assert_eq!(second.severity, Some(severity::WARNING));
	}

	#[test]
	fn reads_every_printed_level() {
		let out = parse(SAMPLES[1].stdout, b"", 1);
		let levels: Vec<_> = out.iter().map(|d| d.severity).collect();
		assert_eq!(
			levels,
			[
				Some(severity::ERROR),
				Some(severity::HINT),
				Some(severity::ERROR),
				Some(severity::INFO)
			]
		);
	}

	#[test]
	fn an_unknown_severity_has_no_level() {
		let json = br#"{"files":[{"path":"a.rb","offenses":[{"severity":"note","message":"m","cop_name":"X/Y",
            "location":{"start_line":1,"start_column":1,"last_line":1,"last_column":1}}]}]}"#;
		assert_eq!(parse(json, b"", 1)[0].severity, None);
	}

	#[test]
	fn no_files_yields_nothing() {
		assert!(parse(br#"{"files":[]}"#, b"", 0).is_empty());
		assert!(parse(b"not json", b"", 0).is_empty());
	}
	#[test]
	fn an_end_without_a_successor_is_dropped() {
		let json = br#"{"files":[{"path":"a.rb","offenses":[{"severity":"warning","message":"m","cop_name":"X/Y",
            "location":{"start_line":1,"start_column":1,"last_line":1,"last_column":4294967295}}]}]}"#;
		assert_eq!(parse(json, b"", 1)[0].end_col, None);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{
      "metadata": {"rubocop_version": "1.65.0"},
      "files": [
        {
          "path": "foo.rb",
          "offenses": [
            {
              "severity": "convention",
              "message": "Use 2 spaces for indentation.",
              "cop_name": "Layout/IndentationWidth",
              "corrected": false,
              "correctable": true,
              "location": {
                "start_line": 3,
                "start_column": 1,
                "last_line": 3,
                "last_column": 4,
                "length": 4,
                "line": 3,
                "column": 1
              }
            },
            {
              "severity": "warning",
              "message": "Unused method argument - x.",
              "cop_name": "Lint/UnusedMethodArgument",
              "corrected": false,
              "correctable": true,
              "location": {
                "start_line": 5,
                "start_column": 10,
                "last_line": 7,
                "last_column": 2,
                "length": 30,
                "line": 5,
                "column": 10
              }
            }
          ]
        }
      ],
      "summary": {"offense_count": 2, "target_file_count": 1, "inspected_file_count": 1}
    }"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"{"metadata":{"rubocop_version":"1.65.0"},"files":[{"path":"foo.rb","offenses":[
      {"severity":"error","message":"Security/Eval: The use of `eval` is a serious security risk.","cop_name":"Security/Eval","corrected":false,"correctable":false,
       "location":{"start_line":9,"start_column":1,"last_line":9,"last_column":4,"length":4,"line":9,"column":1}},
      {"severity":"refactor","message":"Method has too many lines. [12/10]","cop_name":"Metrics/MethodLength","corrected":false,"correctable":false,
       "location":{"start_line":2,"start_column":3,"last_line":14,"last_column":5,"length":210,"line":2,"column":3}},
      {"severity":"fatal","message":"unexpected token kEND","cop_name":"Lint/Syntax","corrected":false,"correctable":false,
       "location":{"start_line":20,"start_column":1,"last_line":20,"last_column":3,"length":3,"line":20,"column":1}},
      {"severity":"info","message":"Prefer single-quoted strings.","cop_name":"Style/StringLiterals","corrected":false,"correctable":true,
       "location":{"start_line":4,"start_column":7,"last_line":4,"last_column":12,"length":6,"line":4,"column":7}}
    ]}],"summary":{"offense_count":4,"target_file_count":1,"inspected_file_count":1}}"#,
		stderr: b"",
		exit: 1,
	},
];
