//! protolint — a pluggable linter and fixer for Protocol Buffer style.
//! Ported from the none-ls diagnostics/protolint builtin.
//!
//! protolint runs with `--reporter json` and emits a JSON object `{"lints":[…]}`
//! (read `from_stderr`). Each lint carries `message`, `line`, `column`, and a
//! `rule` (used as `code`); every lint is reported at severity warning. The
//! builtin slices from the first `{` and, when no `{` is found or JSON decoding
//! fails, reports the whole output as one generic error on row 1. The
//! `FILE_NAMES_LOWER_SNAKE_CASE` rule is skipped — it is a false positive caused
//! by linting via a temp file.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "protolint",
	description: "A pluggable linter and fixer to enforce Protocol Buffer style and conventions.",
	url: "https://github.com/yoheimuta/protolint",
	operations: &[Operation {
		mode: "lint",
		// to_temp_file: protolint does not accept stdin, so it lints the file path.
		args: &["--reporter", "json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true: protolint writes its report to stderr.
	let stderr = String::from_utf8_lossy(stderr);
	let stdout = String::from_utf8_lossy(stdout);
	let output = if stderr.trim().is_empty() {
		stdout.as_ref()
	} else {
		stderr.as_ref()
	};

	if output.trim().is_empty() {
		return Vec::new();
	}

	// Slice from the first `{`; without one, nothing parseable — generic issue.
	let Some(json_index) = output.find('{') else {
		return vec![generic_issue(output)];
	};
	let maybe_json = &output[json_index..];

	let decoded: JsonValue = match maybe_json.parse() {
		Ok(v) => v,
		Err(_) => return vec![generic_issue(output)],
	};

	let lints = match &decoded {
		JsonValue::Object(map) => match map.get("lints") {
			Some(JsonValue::Array(items)) => items,
			// decoded but no lints array -> nothing to report
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};

	lints.iter().filter_map(from_lint).collect()
}

fn from_lint(lint: &JsonValue) -> Option<RawDiagnostic> {
	let map = match lint {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let rule = match map.get("rule") {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	};
	// Skip the temp-file false positive (see module docs).
	if rule.as_deref() == Some("FILE_NAMES_LOWER_SNAKE_CASE") {
		return None;
	}
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	Some(RawDiagnostic {
		message,
		row: get_u32(map, "line"),
		col: get_u32(map, "column"),
		code: rule,
		severity: Some(severity::WARNING),
		source: Some("protolint".to_string()),
		..RawDiagnostic::default()
	})
}

fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

fn generic_issue(output: &str) -> RawDiagnostic {
	RawDiagnostic {
		message: output.to_string(),
		row: Some(1),
		severity: Some(severity::ERROR),
		source: Some("protolint".to_string()),
		..RawDiagnostic::default()
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_lints_as_warnings() {
		let json = br#"{"lints":[
            {"message":"Field name \"FieldName\" must be underscore_separated_names","line":5,"column":3,"rule":"FIELD_NAMES_LOWER_SNAKE_CASE"},
            {"message":"EnumField name should be CAPITALS_WITH_UNDERSCORES","line":10,"column":1,"rule":"ENUM_FIELD_NAMES_UPPER_SNAKE_CASE"}
        ]}"#;
		let out = parse(b"", json, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].row, Some(5));
		assert_eq!(out[0].col, Some(3));
		assert_eq!(out[0].code.as_deref(), Some("FIELD_NAMES_LOWER_SNAKE_CASE"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].source.as_deref(), Some("protolint"));
	}

	#[test]
	fn skips_file_names_lower_snake_case() {
		let json = br#"{"lints":[
            {"message":"File name should be lower_snake_case.proto","line":1,"column":1,"rule":"FILE_NAMES_LOWER_SNAKE_CASE"},
            {"message":"real issue","line":2,"column":1,"rule":"INDENT"}
        ]}"#;
		let out = parse(b"", json, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "real issue");
	}

	#[test]
	fn non_json_output_becomes_generic_error() {
		let out = parse(b"", b"protolint: failed to parse proto file", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert!(out[0].message.contains("failed to parse"));
	}
}
