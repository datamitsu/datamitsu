//! terragrunt_validate — validate Terragrunt configuration files in a directory.
//! Ported from the none-ls diagnostics/terragrunt_validate builtin.
//!
//! Runs `terragrunt hclvalidate --terragrunt-hclvalidate-json` and reads JSON from
//! stderr (`from_stderr = true`). Each diagnostic has `summary` (the message),
//! an optional `detail` (appended as `summary - detail`), a string `severity`, and
//! an optional `range` with `start`/`end` `{line, column}` positions. none-ls
//! defaults row/col to 0 when no `range` is present; we leave them `None` and let
//! the core supply its fallback.
use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "terragrunt_validate",
	description: "Terragrunt validate is is a subcommand of terragrunt to validate configuration files in a directory",
	url: "https://terragrunt.gruntwork.io/docs/reference/cli-options/#validate-inputs",
	operations: &[Operation {
		mode: "lint",
		args: &["hclvalidate", "--terragrunt-hclvalidate-json"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stderr);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	// terragrunt emits `{"diagnostics":[...]}`; tolerate a bare array too.
	let items = match &value {
		JsonValue::Object(root) => match root.get("diagnostics") {
			Some(JsonValue::Array(a)) => a,
			_ => return Vec::new(),
		},
		JsonValue::Array(a) => a,
		_ => return Vec::new(),
	};
	items.iter().filter_map(parse_diagnostic).collect()
}

fn parse_diagnostic(value: &JsonValue) -> Option<RawDiagnostic> {
	let obj = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let summary = str_field(obj, "summary")?;
	let message = match str_field(obj, "detail") {
		Some(detail) => format!("{summary} - {detail}"),
		None => summary,
	};
	let severity = str_field(obj, "severity").and_then(|s| severity_of(&s));

	// Positions only exist when a `range` is present.
	let (row, col, end_row, end_col) = match obj.get("range") {
		Some(JsonValue::Object(range)) => {
			let (row, col) = pos(range.get("start"));
			let (end_row, end_col) = pos(range.get("end"));
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
		severity,
		source: Some("terragrunt validate".to_string()),
		..RawDiagnostic::default()
	})
}

fn pos(value: Option<&JsonValue>) -> (Option<u32>, Option<u32>) {
	match value {
		Some(JsonValue::Object(m)) => (u32_field(m, "line"), u32_field(m, "column")),
		_ => (None, None),
	}
}

fn str_field(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn u32_field(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

/// none-ls `h.diagnostics.severities`: error=1, warning=2, information=3, hint=4.
fn severity_of(level: &str) -> Option<u8> {
	match level {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		"information" => Some(severity::INFO),
		"hint" => Some(severity::HINT),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_diagnostic_with_range_and_detail() {
		let json = br#"{"diagnostics":[
            {"severity":"error","summary":"Unsupported argument",
             "detail":"An argument named \"foo\" is not expected here.",
             "range":{"filename":"terragrunt.hcl",
                      "start":{"line":12,"column":3,"byte":120},
                      "end":{"line":12,"column":6,"byte":123}}}
        ]}"#;
		let out = parse(b"", json, 0);
		assert_eq!(out.len(), 1);
		assert_eq!(
			out[0].message,
			"Unsupported argument - An argument named \"foo\" is not expected here."
		);
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(3));
		assert_eq!(out[0].end_row, Some(12));
		assert_eq!(out[0].end_col, Some(6));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("terragrunt validate"));
	}

	#[test]
	fn parses_diagnostic_without_range_or_detail() {
		let json = br#"{"diagnostics":[{"severity":"warning","summary":"Deprecated block"}]}"#;
		let out = parse(b"", json, 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Deprecated block");
		assert_eq!(out[0].row, None);
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn empty_diagnostics_yields_nothing() {
		assert!(parse(b"", br#"{"diagnostics":[]}"#, 0).is_empty());
	}
}
