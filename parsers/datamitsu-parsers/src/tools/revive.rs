//! revive — Fast, configurable, extensible, flexible, and beautiful linter for Go.
//! Ported from the none-ls diagnostics/revive builtin.
//!
//! revive's JSON output is a top-level array of failures:
//! `[{"Severity":"warning","Failure":"...","RuleName":"exported","Position":{"Start":{"Line":3,"Column":7},"End":{"Line":3,"Column":12}}}]`.
//! `Failure` is the message, `Severity` (error/warning) the level, `RuleName`
//! the code, and `Position.Start` / `Position.End` the span: go/token positions,
//! 1-based, with `End` just past the node (exclusive).
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "revive",
	description: "Fast, configurable, extensible, flexible, and beautiful linter for Go.",
	url: "https://revive.run/",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	// to_stdin=false and the builtin lints the whole package (`./...`); there is no
	// per-file/stdin placeholder. revive does not accept extra non-file args after
	// the target, so this is the full canonical invocation.
	operations: &[Operation {
		mode: "lint",
		args: &["-formatter", "json", "./..."],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	let items = match &value {
		JsonValue::Array(items) => items,
		_ => return Vec::new(),
	};

	items.iter().filter_map(failure_to_diag).collect()
}

fn failure_to_diag(item: &JsonValue) -> Option<RawDiagnostic> {
	let map = match item {
		JsonValue::Object(m) => m,
		_ => return None,
	};

	let message = get_str(map, "Failure")?;

	let file = match map.get("Position") {
		Some(JsonValue::Object(pos)) => match pos.get("Start") {
			Some(JsonValue::Object(s)) => get_str(s, "Filename")
				.as_deref()
				.and_then(crate::diagnostic::exact_file_field),
			_ => None,
		},
		_ => None,
	};
	let (row, col, end_row, end_col) = match map.get("Position") {
		Some(JsonValue::Object(pos)) => {
			let (row, col) = match pos.get("Start") {
				Some(JsonValue::Object(s)) => (get_u32(s, "Line"), get_u32(s, "Column")),
				_ => (None, None),
			};
			let (end_row, end_col) = match pos.get("End") {
				Some(JsonValue::Object(e)) => (get_u32(e, "Line"), get_u32(e, "Column")),
				_ => (None, None),
			};
			(row, col, end_row, end_col)
		}
		_ => (None, None, None, None),
	};

	Some(RawDiagnostic {
		message,
		row,
		col,
		end_row,
		end_col,
		severity: get_str(map, "Severity").and_then(|s| severity::of(DESCRIPTOR.severities, &s)),
		source: Some("revive".to_string()),
		code: get_str(map, "RuleName").filter(|r| !r.is_empty()),
		file,
		..RawDiagnostic::default()
	})
}

fn get_str(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_failures_with_nested_position() {
		let out = parse(SAMPLES[0].stdout, b"", 0);
		assert_eq!(out.len(), 2);
		assert_eq!(
			out[0].message,
			"exported function Foo should have comment or be unexported"
		);
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_row, Some(12));
		assert_eq!(out[0].end_col, Some(20));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].source.as_deref(), Some("revive"));
		assert_eq!(out[0].code.as_deref(), Some("exported"));
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[1].code.as_deref(), Some("redefines-builtin-id"));
		assert_eq!(out[1].row, Some(3));
	}

	#[test]
	fn an_unknown_severity_has_no_level() {
		let json = br#"[{"Severity":"","Failure":"f","RuleName":"r","Position":{"Start":{"Line":1,"Column":1},"End":{"Line":1,"Column":2}}}]"#;
		let out = parse(json, b"", 0);
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 1).is_empty());
	}
	#[test]
	fn names_the_file_of_each_failure() {
		let json = br#"[{"Severity":"warning","Failure":"f","RuleName":"exported","Position":{"Start":{"Filename":"pkg/a.go","Line":3,"Column":1},"End":{"Filename":"pkg/a.go","Line":3,"Column":4}}}]"#;
		assert_eq!(parse(json, b"", 1)[0].file.as_deref(), Some("pkg/a.go"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"[{"Severity":"warning","Failure":"exported function Foo should have comment or be unexported","RuleName":"exported","Category":"comments","Position":{"Start":{"Filename":"main.go","Offset":120,"Line":12,"Column":1},"End":{"Filename":"main.go","Offset":139,"Line":12,"Column":20}},"Confidence":1,"ReplacementLine":""},{"Severity":"error","Failure":"redefinition of the built-in function len","RuleName":"redefines-builtin-id","Category":"logic","Position":{"Start":{"Filename":"main.go","Offset":30,"Line":3,"Column":5},"End":{"Filename":"main.go","Offset":33,"Line":3,"Column":8}},"Confidence":1,"ReplacementLine":""}]"#,
		stderr: b"",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: br#"[{"Severity":"error","Failure":"should have a package comment","RuleName":"package-comments","Category":"comments","Position":{"Start":{"Filename":"main.go","Offset":0,"Line":1,"Column":1},"End":{"Filename":"main.go","Offset":12,"Line":1,"Column":13}},"Confidence":0.2,"ReplacementLine":""}]"#,
		stderr: b"",
		exit: 1,
	},
];
