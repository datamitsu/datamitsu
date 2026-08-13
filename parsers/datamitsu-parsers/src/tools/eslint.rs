//! eslint — the pluggable JavaScript/TypeScript linter.
//!
//! eslint is NOT a none-ls diagnostics builtin (it was moved to an external
//! plugin), so this is ported directly from eslint's `--format json` output: an
//! array of result objects, each with a `messages` array. Two wrinkles make it a
//! bespoke parser rather than a `json_diag::from_json` one-liner:
//!   * diagnostics are **nested** under each file's `messages`, and
//!   * `severity` is **numeric** (2 = error, 1 = warning), not a string token;
//!   * `ruleId` may be `null` (parse/internal errors) → no code.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "eslint",
	description: "Pluggable linter for JavaScript and TypeScript.",
	url: "https://eslint.org",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// Lenient: eslint runs batched over a whole project, where a plugin writing to
	// stdout (sonarjs' pnpm-catalog `console.debug`, …) would otherwise cost every
	// diagnostic in the run.
	crate::tools::json_diag::extract_lenient(stdout, from_report)
}

fn from_report(value: &JsonValue) -> Vec<RawDiagnostic> {
	let results = match value {
		JsonValue::Array(a) => a,
		_ => return Vec::new(),
	};
	let mut out = Vec::new();
	for result in results {
		let obj = match result {
			JsonValue::Object(m) => m,
			_ => continue,
		};
		// One eslint run covers many files, so each result's path is the only way
		// to attribute its messages.
		let file = match obj.get("filePath") {
			Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
			_ => None,
		};
		if let Some(JsonValue::Array(messages)) = obj.get("messages") {
			for msg in messages {
				if let Some(mut d) = message_to_diag(msg) {
					d.file.clone_from(&file);
					out.push(d);
				}
			}
		}
	}
	out
}

fn message_to_diag(msg: &JsonValue) -> Option<RawDiagnostic> {
	let m = match msg {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = match m.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	Some(RawDiagnostic {
		message,
		row: num(m, "line"),
		col: num(m, "column"),
		end_row: num(m, "endLine"),
		end_col: num(m, "endColumn"),
		severity: severity_of(m.get("severity")),
		code: match m.get("ruleId") {
			Some(JsonValue::String(s)) => Some(s.clone()),
			_ => None, // null for parse/internal errors
		},
		..RawDiagnostic::default()
	})
}

fn num(m: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match m.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

fn severity_of(v: Option<&JsonValue>) -> Option<u8> {
	match v {
		Some(JsonValue::Number(n)) => match crate::numconv::json_int(*n) {
			Some(2) => Some(severity::ERROR),
			Some(1) => Some(severity::WARNING),
			_ => None,
		},
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	// Real `eslint --format json` output (trimmed).
	const SAMPLE: &[u8] = br#"[{"filePath":"/x/broken.js","messages":[
        {"ruleId":"no-unused-vars","severity":2,"message":"'x' is assigned a value but never used.","line":1,"column":5,"endLine":1,"endColumn":6},
        {"ruleId":"semi","severity":1,"message":"Missing semicolon.","line":1,"column":10,"endLine":2,"endColumn":1},
        {"ruleId":null,"severity":2,"message":"Parsing error: Unexpected token","line":3,"column":1}
    ]}]"#;

	#[test]
	fn parses_nested_messages_with_numeric_severity() {
		let out = parse(SAMPLE, b"", 1);
		assert_eq!(out.len(), 3);

		assert_eq!(out[0].message, "'x' is assigned a value but never used.");
		assert_eq!((out[0].row, out[0].col), (Some(1), Some(5)));
		assert_eq!((out[0].end_row, out[0].end_col), (Some(1), Some(6)));
		assert_eq!(out[0].severity, Some(severity::ERROR)); // eslint 2 -> error
		assert_eq!(out[0].code.as_deref(), Some("no-unused-vars"));

		assert_eq!(out[1].severity, Some(severity::WARNING)); // eslint 1 -> warning
		assert_eq!(out[1].code.as_deref(), Some("semi"));

		// null ruleId -> no code; still a diagnostic.
		assert_eq!(out[2].code, None);
		assert_eq!(out[2].message, "Parsing error: Unexpected token");
	}

	#[test]
	fn attributes_each_message_to_its_file() {
		let out = parse(SAMPLE, b"", 1);
		assert!(out.iter().all(|d| d.file.as_deref() == Some("/x/broken.js")));
	}

	#[test]
	fn skips_plugin_noise_printed_before_the_json() {
		// eslint-plugin-sonarjs console.debug()s onto stdout ahead of the report.
		let mut noisy = b"Dependency \"@scope/pkg\" could not be resolved for catalog \"default\"\n".to_vec();
		noisy.extend_from_slice(SAMPLE);
		assert_eq!(parse(&noisy, b"", 1).len(), 3);
	}

	#[test]
	fn clean_run_yields_nothing() {
		assert!(parse(br#"[{"filePath":"/x/ok.js","messages":[]}]"#, b"", 0).is_empty());
	}
}
