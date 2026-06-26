//! semgrep — static analysis for bugs and code standards. Ported from the
//! none-ls diagnostics/semgrep builtin.
//!
//! Semgrep's `--json` output nests diagnostics under `results[]`, with the
//! message/severity inside `extra` and the span split across `start`/`end`
//! objects, so this parser walks the structure directly (the flat
//! `json_diag::from_json` mapper can't reach the nested fields). Field mapping
//! mirrors the builtin's `handle_semgrep_output`: message=extra.message,
//! ruleId=check_id, level=extra.severity, line/column=start.line/start.col,
//! endLine/endColumn=end.line/end.col.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "semgrep",
	description: "Semgrep is a fast, open-source, static analysis tool for finding bugs and \
        enforcing code standards at editor, commit, and CI time.",
	url: "https://semgrep.dev/",
	operations: &[Operation {
		mode: "lint",
		args: &["-q", "--json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let results = match &value {
		JsonValue::Object(m) => match m.get("results") {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};
	results.iter().filter_map(from_result).collect()
}

fn from_result(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let extra = obj(map.get("extra"));
	let message = extra.and_then(|e| get_str(e, "message"))?;
	let start = obj(map.get("start"));
	let end = obj(map.get("end"));
	Some(RawDiagnostic {
		message,
		code: get_str(map, "check_id"),
		severity: extra.and_then(|e| get_str(e, "severity")).and_then(|s| severity_of(&s)),
		row: start.and_then(|s| get_u32(s, "line")),
		col: start.and_then(|s| get_u32(s, "col")),
		end_row: end.and_then(|e| get_u32(e, "line")),
		end_col: end.and_then(|e| get_u32(e, "col")),
		..RawDiagnostic::default()
	})
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"INFO" => Some(severity::INFO),
		"WARNING" => Some(severity::WARNING),
		"ERROR" => Some(severity::ERROR),
		_ => None,
	}
}

fn obj(v: Option<&JsonValue>) -> Option<&std::collections::HashMap<String, JsonValue>> {
	match v {
		Some(JsonValue::Object(m)) => Some(m),
		_ => None,
	}
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
	fn parses_nested_result() {
		let json = br#"{
            "results": [
                {
                    "check_id": "python.lang.security.audit.dangerous-system-call",
                    "start": {"line": 10, "col": 5},
                    "end": {"line": 10, "col": 30},
                    "extra": {
                        "message": "Found dangerous system call",
                        "severity": "ERROR"
                    }
                }
            ],
            "errors": []
        }"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Found dangerous system call");
		assert_eq!(
			out[0].code.as_deref(),
			Some("python.lang.security.audit.dangerous-system-call")
		);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].row, Some(10));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_row, Some(10));
		assert_eq!(out[0].end_col, Some(30));
	}

	#[test]
	fn maps_severity_tokens() {
		assert_eq!(severity_of("INFO"), Some(severity::INFO));
		assert_eq!(severity_of("WARNING"), Some(severity::WARNING));
		assert_eq!(severity_of("ERROR"), Some(severity::ERROR));
		assert_eq!(severity_of("other"), None);
	}

	#[test]
	fn no_results_yields_nothing() {
		assert!(parse(br#"{"results": []}"#, b"", 0).is_empty());
		assert!(parse(b"not json", b"", 1).is_empty());
	}
}
