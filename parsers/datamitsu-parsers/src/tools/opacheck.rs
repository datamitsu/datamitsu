//! opacheck — check Rego source files for parse and compilation errors.
//! Ported from the none-ls diagnostics/opacheck builtin.
//!
//! OPA's `check -f json` emits `{"errors":[{message, code, location:{file,row,col}}]}`.
//! The builtin keeps only entries that carry a `location` (others are global,
//! non-diagnostic errors). The `errors` key is the level: every entry under it is
//! an error.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "opacheck",
	description: "Check Rego source files for parse and compilation errors.",
	url: "https://www.openpolicyagent.org/docs/latest/cli/#opa-check",
	severities: &[Level(ERRORS, severity::ERROR)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"check",
			"-f",
			"json",
			"--strict",
			"{file}",
			"--ignore=*.yaml",
			"--ignore=*.yml",
			"--ignore=*.json",
			"--ignore=.git/**/*",
		],
		stdin: false,
	}],
};

/// The report's key for its findings, which is also their level token.
const ERRORS: &str = "errors";

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true: OPA reports check failures on stderr.
	let mut out = parse_bytes(stderr);
	if out.is_empty() {
		out = parse_bytes(stdout);
	}
	out
}

fn parse_bytes(bytes: &[u8]) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(bytes);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let errors = match &value {
		JsonValue::Object(m) => match m.get(ERRORS) {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};
	let level = severity::of(DESCRIPTOR.severities, ERRORS);
	errors.iter().filter_map(|e| diag_from_error(e, level)).collect()
}

fn diag_from_error(value: &JsonValue, severity: Option<u8>) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	// Only entries with a location are surfaced as diagnostics.
	let location = match map.get("location") {
		Some(JsonValue::Object(loc)) => loc,
		_ => return None,
	};
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	let row = location.get("row").and_then(as_u32);
	let col = location.get("col").and_then(as_u32);
	let code = match map.get("code") {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	};
	Some(RawDiagnostic {
		message,
		row,
		col,
		severity,
		source: Some("opacheck".to_string()),
		code,
		file: match location.get("file") {
			Some(JsonValue::String(s)) => crate::diagnostic::file_field(s),
			_ => None,
		},
		..RawDiagnostic::default()
	})
}

fn as_u32(v: &JsonValue) -> Option<u32> {
	match v {
		JsonValue::Number(n) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_with_location() {
		let out = parse(&[], SAMPLES[0].stderr, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "rego_parse_error: unexpected eof");
		assert_eq!(out[0].row, Some(4));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[0].code.as_deref(), Some("rego_parse_error"));
		assert_eq!(out[0].source.as_deref(), Some("opacheck"));
	}

	#[test]
	fn an_entry_under_errors_is_an_error() {
		let out = parse(&[], SAMPLES[0].stderr, 1);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		let from_stdout = parse(SAMPLES[0].stderr, &[], 1);
		assert_eq!(from_stdout[0].severity, Some(severity::ERROR));
	}

	#[test]
	fn skips_errors_without_location() {
		let json = br#"{"errors":[{"message":"loading error: no files found"}]}"#;
		let out = parse(&[], json, 1);
		assert!(out.is_empty());
	}

	#[test]
	fn empty_or_missing_errors_yields_nothing() {
		assert!(parse(&[], br#"{"errors":null}"#, 0).is_empty());
		assert!(parse(&[], b"not json", 1).is_empty());
	}

	#[test]
	fn each_error_names_its_file() {
		let json = br#"{"errors":[
            {"message":"first","code":"c","location":{"file":"a.rego","row":1,"col":1}},
            {"message":"second","code":"c","location":{"file":"lib/b.rego","row":2,"col":1}}]}"#;
		let out = parse(&[], json, 1);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(got, [("first", Some("a.rego")), ("second", Some("lib/b.rego"))]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"",
	stderr: br#"{
  "errors": [
    {
      "message": "rego_parse_error: unexpected eof",
      "code": "rego_parse_error",
      "location": {
        "file": "policy.rego",
        "row": 4,
        "col": 1
      }
    }
  ]
}"#,
	exit: 1,
}];
