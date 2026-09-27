//! proselint — An English prose linter. Ported from the none-ls
//! diagnostics/proselint builtin.
//!
//! proselint's JSON is nested and custom, so this does not use `json_diag`:
//! `output.result` is a map of file → `{ diagnostics: [ ... ] }`, and each
//! diagnostic carries `pos` ([line, col], 1-based), `span` ([start, end), offsets
//! into the whole text), `check_path` (the rule), and `message`. The span's
//! length gives the end column. The builtin bails out when the top-level
//! `output.error` is set. proselint prints no per-diagnostic level, so no finding
//! carries one.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "proselint",
	description: "An English prose linter.",
	url: "https://github.com/amperser/proselint",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["check", "--output-format=json"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let root = match &value {
		JsonValue::Object(m) => m,
		_ => return Vec::new(),
	};

	// Builtin: `if output.error then return diags end`.
	if matches!(root.get("error"), Some(v) if !matches!(v, JsonValue::Null)) {
		return Vec::new();
	}

	let result = match root.get("result") {
		Some(JsonValue::Object(m)) => m,
		_ => return Vec::new(),
	};

	let mut out = Vec::new();
	for file_output in result.values() {
		let file_obj = match file_output {
			JsonValue::Object(m) => m,
			_ => continue,
		};
		let diags = match file_obj.get("diagnostics") {
			Some(JsonValue::Array(a)) => a,
			_ => continue,
		};
		for d in diags {
			if let Some(diag) = from_diag(d) {
				out.push(diag);
			}
		}
	}
	out
}

fn from_diag(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};

	// pos = [line, col]
	let pos = array_of(map, "pos");
	let row = pos.and_then(|a| number_at(a, 0));
	let col = pos.and_then(|a| number_at(a, 1));

	// `pos` is where `span` starts, so the span's length is its extent on that line.
	let span = array_of(map, "span");
	let length = match (span.and_then(|a| number_at(a, 0)), span.and_then(|a| number_at(a, 1))) {
		(Some(start), Some(end)) => end.checked_sub(start),
		_ => None,
	};
	let end_col = col.zip(length).and_then(|(c, l)| c.checked_add(l));

	let code = match map.get("check_path") {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	};

	Some(RawDiagnostic {
		message,
		row,
		col,
		end_col,
		code,
		..RawDiagnostic::default()
	})
}

fn array_of<'a>(map: &'a std::collections::HashMap<String, JsonValue>, key: &str) -> Option<&'a Vec<JsonValue>> {
	match map.get(key) {
		Some(JsonValue::Array(a)) => Some(a),
		_ => None,
	}
}

fn number_at(arr: &[JsonValue], idx: usize) -> Option<u32> {
	match arr.get(idx) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_nested_diagnostics() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Use the curly quote.");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_row, None);
		assert_eq!(out[0].end_col, Some(12));
		assert_eq!(out[0].code.as_deref(), Some("typography.symbols.curly_quotes"));
	}

	#[test]
	fn the_end_column_comes_from_the_span_length() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out[1].row, Some(7));
		assert_eq!(out[1].col, Some(1));
		assert_eq!(out[1].end_col, Some(12));
	}

	#[test]
	fn never_sets_a_severity() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert!(out.iter().all(|d| d.severity.is_none()), "{out:?}");
	}

	#[test]
	fn top_level_error_yields_nothing() {
		let json =
			br#"{"error": "boom", "result": {"f": {"diagnostics": [{"message": "x", "pos": [1, 1], "span": [1, 2]}]}}}"#;
		assert!(parse(json, b"", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 1).is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"{
    "result": {
        "<stdin>": {
            "diagnostics": [
                {
                    "check_path": "typography.symbols.curly_quotes",
                    "message": "Use the curly quote.",
                    "pos": [3, 5],
                    "replacements": null,
                    "span": [45, 52]
                },
                {
                    "check_path": "uncomparables",
                    "message": "Comparison of an uncomparable: 'very unique' is not comparable.",
                    "pos": [7, 1],
                    "replacements": null,
                    "span": [120, 131]
                }
            ]
        }
    }
}"#,
	stderr: b"",
	exit: 1,
}];
