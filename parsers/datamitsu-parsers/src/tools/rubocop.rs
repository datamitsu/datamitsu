//! rubocop — The Ruby Linter/Formatter that Serves and Protects. Ported from the
//! none-ls diagnostics/rubocop builtin.
//!
//! Unlike the flat JSON tools, rubocop nests its diagnostics under
//! `output.files[0].offenses[]`, and each offense carries its span in a nested
//! `location` object (`start_line`/`start_column`/`last_line`/`last_column`).
//! none-ls flattens these into the standard diagnostic fields, with one quirk: a
//! multi-line offense (`start_line != last_line`) collapses the end position to
//! `endLine = start_line, endColumn = 0`. We reproduce that here against tinyjson
//! directly since the shared flat `from_json` helper cannot navigate the nesting.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "rubocop",
	description: "The Ruby Linter/Formatter that Serves and Protects.",
	url: "https://rubocop.org/",
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

	// none-ls quirk: a multi-line offense collapses the end span to the start
	// line, column 0.
	let (end_row, end_col) = match (start_line, last_line) {
		(Some(s), Some(e)) if s != e => (Some(s), Some(0)),
		_ => (last_line, last_col),
	};

	Some(RawDiagnostic {
		message,
		row: start_line,
		col: start_col,
		end_row,
		end_col,
		code: get_str(map, "cop_name"),
		severity: get_str(map, "severity").as_deref().and_then(severity_of),
		..RawDiagnostic::default()
	})
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"info" | "convention" => Some(severity::INFO),
		"refactor" => Some(severity::HINT),
		"warning" => Some(severity::WARNING),
		// none-ls maps "fatal" to its own fatal level; our scale tops out at ERROR.
		"error" | "fatal" => Some(severity::ERROR),
		_ => None,
	}
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

	const SAMPLE: &[u8] = br#"{
      "metadata": {"rubocop_version": "1.0.0"},
      "files": [
        {
          "path": "foo.rb",
          "offenses": [
            {
              "severity": "convention",
              "message": "Use 2 spaces for indentation.",
              "cop_name": "Layout/IndentationWidth",
              "location": {
                "start_line": 3,
                "start_column": 1,
                "last_line": 3,
                "last_column": 4
              }
            },
            {
              "severity": "warning",
              "message": "Unused method argument - x.",
              "cop_name": "Lint/UnusedMethodArgument",
              "location": {
                "start_line": 5,
                "start_column": 10,
                "last_line": 7,
                "last_column": 2
              }
            }
          ]
        }
      ]
    }"#;

	#[test]
	fn parses_offenses_with_location_and_severity() {
		let out = parse(SAMPLE, b"", 1);
		assert_eq!(out.len(), 2);

		let first = &out[0];
		assert_eq!(first.message, "Use 2 spaces for indentation.");
		assert_eq!(first.row, Some(3));
		assert_eq!(first.col, Some(1));
		assert_eq!(first.end_row, Some(3));
		assert_eq!(first.end_col, Some(4));
		assert_eq!(first.code.as_deref(), Some("Layout/IndentationWidth"));
		assert_eq!(first.severity, Some(severity::INFO));
	}

	#[test]
	fn multiline_offense_collapses_end_span() {
		let out = parse(SAMPLE, b"", 1);
		let second = &out[1];
		assert_eq!(second.row, Some(5));
		assert_eq!(second.col, Some(10));
		// start_line (5) != last_line (7) -> endLine=start_line, endColumn=0.
		assert_eq!(second.end_row, Some(5));
		assert_eq!(second.end_col, Some(0));
		assert_eq!(second.severity, Some(severity::WARNING));
	}

	#[test]
	fn no_files_yields_nothing() {
		assert!(parse(br#"{"files":[]}"#, b"", 0).is_empty());
		assert!(parse(b"not json", b"", 0).is_empty());
	}
}
