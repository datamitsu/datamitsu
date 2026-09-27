//! ktlint — an anti-bikeshedding Kotlin linter with built-in formatter.
//! Ported from the none-ls diagnostics/ktlint builtin.
//!
//! ktlint's `--reporter=json` emits a per-file array, each entry holding a nested
//! `errors` array — not the flat none-ls default JSON — so the navigation is
//! hand-written over `tinyjson` rather than via `json_diag::from_json`.
//! The `errors` key is the level: every entry under it is an error.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "ktlint",
	description: "An anti-bikeshedding Kotlin linter with built-in formatter.",
	url: "https://ktlint.github.io/",
	severities: &[Level(ERRORS, severity::ERROR)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"--relative",
			"--reporter=json",
			"--log-level=none",
			"**/*.kt",
			"**/*.kts",
		],
		stdin: true,
	}],
};

const ERRORS: &str = "errors";

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let files = match &value {
		JsonValue::Array(items) => items,
		_ => return Vec::new(),
	};

	let level = severity::of(DESCRIPTOR.severities, ERRORS);
	let mut out = Vec::new();
	for file in files {
		let JsonValue::Object(file) = file else {
			continue;
		};
		let Some(JsonValue::Array(errors)) = file.get(ERRORS) else {
			continue;
		};
		let path = match file.get("file") {
			Some(JsonValue::String(s)) => crate::diagnostic::file_field(s),
			_ => None,
		};
		for err in errors {
			if let Some(mut d) = from_error(err) {
				d.file.clone_from(&path);
				d.severity = level;
				out.push(d);
			}
		}
	}
	out
}

fn from_error(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	let code = match map.get("rule") {
		Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
		_ => None,
	};
	Some(RawDiagnostic {
		message,
		row: get_u32(map, "line"),
		col: get_u32(map, "column"),
		source: Some("ktlint".to_string()),
		code,
		..RawDiagnostic::default()
	})
}

fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		Some(JsonValue::String(s)) => s.trim().parse().ok(),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_nested_errors() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "Unexpected blank line(s) before \"}\"");
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[0].code.as_deref(), Some("standard:no-blank-line-before-rbrace"));
		assert_eq!(out[0].source.as_deref(), Some("ktlint"));

		assert_eq!(out[1].code, None);
		assert_eq!(out[1].row, Some(5));
	}

	#[test]
	fn an_entry_under_errors_is_an_error() {
		for exit in [0, 1] {
			let out = parse(SAMPLES[0].stdout, b"", exit);
			assert!(out.iter().all(|d| d.severity == Some(severity::ERROR)), "{out:?}");
		}
	}

	#[test]
	fn empty_and_invalid_yield_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
		assert!(parse(b"not json", b"", 1).is_empty());
		// file with no errors array.
		assert!(parse(br#"[{"file":"a.kt"}]"#, b"", 0).is_empty());
	}

	#[test]
	fn each_finding_names_its_file() {
		let json = br#"[
            {"file":"a.kt","errors":[{"line":1,"column":1,"message":"first","rule":"r1"}]},
            {"file":"src/b.kts","errors":[{"line":2,"column":1,"message":"second","rule":"r2"}]},
            {"file":"<stdin>","errors":[{"line":3,"column":1,"message":"piped","rule":"r3"}]}]"#;
		let out = parse(json, b"", 1);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(
			got,
			[("first", Some("a.kt")), ("second", Some("src/b.kts")), ("piped", None)]
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"[
    {
        "file": "src/Main.kt",
        "errors": [
            {"line": 1, "column": 1, "message": "Unexpected blank line(s) before \"}\"", "rule": "standard:no-blank-line-before-rbrace"},
            {"line": 5, "column": 3, "message": "Not a valid Kotlin file (5:3 expecting a top level declaration)", "rule": ""}
        ]
    }
]"#,
	stderr: b"",
	exit: 1,
}];
