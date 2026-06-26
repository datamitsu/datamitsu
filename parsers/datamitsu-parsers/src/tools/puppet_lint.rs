//! puppet_lint — Check that your Puppet manifest conforms to the style guide.
//! Ported from the none-ls diagnostics/puppet_lint builtin.
//!
//! puppet-lint's `--json` emits a JSON array whose elements are themselves arrays
//! of finding objects (one inner array per linted file). Each finding has
//! `line`, `column`, `check` (rule → source), `message`, and `kind` (severity).
//! The nesting and the non-default field names mean we hand-walk the JSON with
//! tinyjson rather than using the flat `json_diag::from_json` helper.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "puppet_lint",
	description: "Check that your Puppet manifest conforms to the style guide.",
	url: "http://puppet-lint.com/",
	operations: &[Operation {
		mode: "lint",
		args: &["--json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	let mut out = Vec::new();
	if let JsonValue::Array(files) = &value {
		for file in files {
			if let JsonValue::Array(findings) = file {
				for f in findings {
					if let Some(d) = from_finding(f) {
						out.push(d);
					}
				}
			}
		}
	}
	out
}

fn from_finding(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = get_str(map, "message")?;
	Some(RawDiagnostic {
		message,
		row: get_u32(map, "line"),
		col: get_u32(map, "column"),
		source: get_str(map, "check"),
		severity: get_str(map, "kind").as_deref().and_then(severity_of),
		..RawDiagnostic::default()
	})
}

fn severity_of(kind: &str) -> Option<u8> {
	match kind {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
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

	#[test]
	fn parses_nested_array_of_findings() {
		let stdout = br#"[
            [
                {"line":1,"column":3,"check":"arrow_alignment","kind":"warning",
                 "message":"indentation of => is not properly aligned","fullpath":"/x/init.pp"},
                {"line":5,"column":1,"check":"hard_tabs","kind":"error",
                 "message":"tab character found","fullpath":"/x/init.pp"}
            ]
        ]"#;
		let out = parse(stdout, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "indentation of => is not properly aligned");
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].col, Some(3));
		assert_eq!(out[0].source.as_deref(), Some("arrow_alignment"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[1].source.as_deref(), Some("hard_tabs"));
		assert_eq!(out[1].severity, Some(severity::ERROR));
	}

	#[test]
	fn unknown_kind_leaves_severity_none() {
		let stdout = br#"[[{"line":2,"column":4,"check":"c","kind":"note","message":"m"}]]"#;
		let out = parse(stdout, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn empty_and_invalid_yield_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
		assert!(parse(b"not json", b"", 0).is_empty());
	}
}
