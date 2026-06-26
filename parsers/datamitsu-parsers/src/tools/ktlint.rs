//! ktlint — an anti-bikeshedding Kotlin linter with built-in formatter.
//! Ported from the none-ls diagnostics/ktlint builtin.
//!
//! ktlint's `--reporter=json` emits a per-file array, each entry holding a nested
//! `errors` array — not the flat none-ls default JSON — so the navigation is
//! hand-written over `tinyjson` rather than via `json_diag::from_json`.
//! Severity follows the builtin: an empty `rule` → ERROR, otherwise WARN.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "ktlint",
	description: "An anti-bikeshedding Kotlin linter with built-in formatter.",
	url: "https://ktlint.github.io/",
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

	let mut out = Vec::new();
	for file in files {
		let errors = match file {
			JsonValue::Object(m) => match m.get("errors") {
				Some(JsonValue::Array(errs)) => errs,
				_ => continue,
			},
			_ => continue,
		};
		for err in errors {
			if let Some(d) = from_error(err) {
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
	let rule = match map.get("rule") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => String::new(),
	};
	// builtin: rule == "" -> ERROR, otherwise WARN.
	let sev = if rule.is_empty() {
		severity::ERROR
	} else {
		severity::WARNING
	};
	let code = if rule.is_empty() { None } else { Some(rule) };
	Some(RawDiagnostic {
		message,
		row: get_u32(map, "line"),
		col: get_u32(map, "column"),
		severity: Some(sev),
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
	fn parses_nested_errors_and_severity() {
		let json = br#"[
            {
                "file": "src/Main.kt",
                "errors": [
                    {"line": 1, "column": 1, "message": "Unexpected blank line(s) before \"}\"", "rule": "no-blank-line-before-rbrace"},
                    {"line": 5, "column": 3, "message": "Something failed", "rule": ""}
                ]
            }
        ]"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "Unexpected blank line(s) before \"}\"");
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].code.as_deref(), Some("no-blank-line-before-rbrace"));
		assert_eq!(out[0].source.as_deref(), Some("ktlint"));

		// empty rule -> ERROR, no code.
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[1].code, None);
		assert_eq!(out[1].row, Some(5));
	}

	#[test]
	fn empty_and_invalid_yield_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
		assert!(parse(b"not json", b"", 1).is_empty());
		// file with no errors array.
		assert!(parse(br#"[{"file":"a.kt"}]"#, b"", 0).is_empty());
	}
}
