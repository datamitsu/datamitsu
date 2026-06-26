//! credo — static analysis of Elixir files for enforcing code consistency.
//! Ported from the none-ls diagnostics/credo builtin.
//!
//! credo emits a JSON object `{"issues":[…]}` (sometimes prefixed by elixir
//! compiler warnings). Each issue carries `message`, `line_no`, optional `column`
//! / `column_end`, and a numeric `priority` that the builtin maps to a severity
//! via dynamic ranges (>=10 error, >=0 warning, >=-10 information, else hint).
//! When no `{` is found, or JSON decoding fails, the whole output becomes one
//! generic diagnostic on row 1.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "credo",
	description: "Static analysis of `elixir` files for enforcing code consistency.",
	url: "https://hexdocs.pm/credo",
	operations: &[Operation {
		mode: "lint",
		args: &["credo", "suggest", "--format", "json", "--read-from-stdin", "{file}"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// credo is tricky: with no elixir warnings its own output is on stdout; with
	// elixir warnings its output stays on stdout while stderr holds the warnings.
	// When stdout is empty, fall back to stderr (mirrors `params.output or params.err`).
	let stdout = String::from_utf8_lossy(stdout);
	let stderr = String::from_utf8_lossy(stderr);
	let output = if stdout.trim().is_empty() {
		stderr.as_ref()
	} else {
		stdout.as_ref()
	};

	if output.trim().is_empty() {
		return Vec::new();
	}

	// Slice from the first `{` — credo may prefix the JSON with compiler warnings.
	let Some(json_index) = output.find('{') else {
		return vec![generic_issue(output)];
	};
	let maybe_json = &output[json_index..];

	let decoded: JsonValue = match maybe_json.parse() {
		Ok(v) => v,
		Err(_) => return vec![generic_issue(output)],
	};

	let issues = match &decoded {
		JsonValue::Object(map) => match map.get("issues") {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};

	issues.iter().filter_map(from_issue).collect()
}

fn from_issue(issue: &JsonValue) -> Option<RawDiagnostic> {
	let map = match issue {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	Some(RawDiagnostic {
		message,
		row: get_u32(map, "line_no"),
		col: get_u32(map, "column"),
		end_col: get_u32(map, "column_end"),
		severity: severity_of(map),
		source: Some("credo".to_string()),
		..RawDiagnostic::default()
	})
}

/// Maps credo's numeric `priority` to the 1–4 scale via the builtin's ranges.
fn severity_of(map: &std::collections::HashMap<String, JsonValue>) -> Option<u8> {
	let priority = match map.get("priority") {
		Some(JsonValue::Number(n)) if n.is_finite() => *n,
		_ => return None,
	};
	Some(if priority >= 10.0 {
		severity::ERROR
	} else if priority >= 0.0 {
		severity::WARNING
	} else if priority >= -10.0 {
		severity::INFO
	} else {
		severity::HINT
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
		source: Some("credo".to_string()),
		..RawDiagnostic::default()
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_issues_with_priority_severity() {
		let json = br#"{"issues":[
            {"message":"Pipe chain should start with a raw value.","line_no":12,"column":5,"column_end":20,"priority":11},
            {"message":"Modules should have a @moduledoc tag.","line_no":1,"priority":1},
            {"message":"low prio note","line_no":3,"priority":-5}
        ]}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 3);
		assert_eq!(out[0].message, "Pipe chain should start with a raw value.");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_col, Some(20));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("credo"));
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].col, None);
		assert_eq!(out[2].severity, Some(severity::INFO));
	}

	#[test]
	fn skips_compiler_warnings_before_json() {
		let json = br#"warning: variable unused
{"issues":[{"message":"x","line_no":2,"priority":0}]}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "x");
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn non_json_output_becomes_generic_issue() {
		let out = parse(b"", b"** (Mix) the task could not be found", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(1));
		assert!(out[0].message.contains("could not be found"));
	}
}
