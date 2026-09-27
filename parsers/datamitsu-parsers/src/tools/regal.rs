//! regal — a linter for Rego. Ported from the none-ls diagnostics/regal builtin.
//!
//! regal emits a JSON object `{"violations":[…]}` (on stderr; the builtin sets
//! `from_stderr = true`). Each violation carries `description`, `level`, `title`
//! (used as the code), `related_resources` (the first `ref` is the rule's
//! documentation) and a nested `location` object with its `file`, 1-based `row` and `col`,
//! the source line as `text`, and — since regal 0.24 — an exclusive `end`
//! `{row, col}`. A violation without a `location` is skipped; one without a
//! `level` has none.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "regal",
	description: "Regal is a linter for Rego, with the goal of making your Rego magnificent!.",
	url: "https://docs.styra.com/regal",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["lint", "-f", "json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// `from_stderr = true`: regal writes its JSON report to stderr. Fall back to
	// stdout if stderr is empty, to be forgiving.
	let stderr = String::from_utf8_lossy(stderr);
	let stdout = String::from_utf8_lossy(stdout);
	let output = if stderr.trim().is_empty() {
		stdout.as_ref()
	} else {
		stderr.as_ref()
	};

	let decoded: JsonValue = match output.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	let violations = match &decoded {
		JsonValue::Object(map) => match map.get("violations") {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};

	violations.iter().filter_map(from_violation).collect()
}

fn from_violation(violation: &JsonValue) -> Option<RawDiagnostic> {
	let map = match violation {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	// A violation without a location is skipped (mirrors `if d.location ~= nil`).
	let location = match map.get("location") {
		Some(JsonValue::Object(loc)) => loc,
		_ => return None,
	};
	let message = get_str(map, "description")?;
	let (end_row, end_col) = match location.get("end") {
		Some(JsonValue::Object(end)) => (get_u32(end, "row"), get_u32(end, "col")),
		_ => (None, None),
	};
	Some(RawDiagnostic {
		message,
		row: get_u32(location, "row"),
		col: get_u32(location, "col"),
		end_row,
		end_col,
		severity: get_str(map, "level").and_then(|l| severity::of(DESCRIPTOR.severities, &l)),
		source: Some("regal".to_string()),
		code: get_str(map, "title"),
		url: documentation(map),
		file: get_str(location, "file")
			.as_deref()
			.and_then(crate::diagnostic::exact_file_field),
	})
}

/// The first `ref` among the violation's `related_resources`.
fn documentation(map: &HashMap<String, JsonValue>) -> Option<String> {
	match map.get("related_resources") {
		Some(JsonValue::Array(items)) => items.iter().find_map(|r| match r {
			JsonValue::Object(r) => get_str(r, "ref"),
			_ => None,
		}),
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
	fn parses_violations_with_location() {
		let out = parse(SAMPLES[0].stdout, SAMPLES[0].stderr, SAMPLES[0].exit);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Prefer snake_case for names");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_row, Some(3));
		assert_eq!(out[0].end_col, Some(10));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("regal"));
		assert_eq!(out[0].code.as_deref(), Some("prefer-snake-case"));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://docs.styra.com/regal/rules/style/prefer-snake-case")
		);
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].url, None);
	}

	#[test]
	fn without_an_end_there_is_no_end() {
		let out = parse(SAMPLES[0].stdout, SAMPLES[0].stderr, SAMPLES[0].exit);
		assert_eq!((out[1].row, out[1].col), (Some(10), Some(1)));
		assert_eq!((out[1].end_row, out[1].end_col), (None, None));
	}

	#[test]
	fn a_missing_level_has_none() {
		let json = br#"{"violations":[
            {"description":"x","title":"r","location":{"row":1,"col":1}}
        ]}"#;
		let out = parse(b"", json, 3);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn violation_without_location_is_skipped() {
		let json = br#"{"violations":[{"description":"orphan","level":"error","title":"r"}]}"#;
		let out = parse(b"", json, 0);
		assert!(out.is_empty());
	}

	#[test]
	fn each_violation_names_its_file() {
		let json = br#"{"violations":[
            {"description":"first","title":"a","location":{"file":"policy/a.rego","row":1,"col":1}},
            {"description":"second","title":"b","location":{"file":"policy/b.rego","row":2,"col":1}}]}"#;
		let out = parse(b"", json, 3);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(
			got,
			[("first", Some("policy/a.rego")), ("second", Some("policy/b.rego"))]
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{"violations":[
            {"title":"prefer-snake-case","description":"Prefer snake_case for names","category":"style","level":"error",
             "related_resources":[{"description":"documentation","ref":"https://docs.styra.com/regal/rules/style/prefer-snake-case"}],
             "location":{"file":"policy.rego","row":3,"col":1,"text":"camelCase := 1","end":{"row":3,"col":10}}},
            {"title":"avoid-importing-input","description":"Avoid importing input","category":"imports","level":"warning",
             "location":{"file":"policy.rego","row":10,"col":1,"text":"import input"}}
        ],"summary":{"files_scanned":1,"files_failed":1,"rules_skipped":0,"num_violations":2}}"#,
		stderr: b"",
		exit: 3,
	},
	crate::contract::Sample {
		stdout: br#"{"violations":[
            {"title":"line-length","description":"Line too long","category":"style","level":"warning",
             "related_resources":[{"description":"documentation","ref":"https://docs.styra.com/regal/rules/style/line-length"}],
             "location":{"file":"policy.rego","row":7,"col":1,"text":"allow if { input.user.name == \"a very long name\" }","end":{"row":7,"col":51}}}
        ],"summary":{"files_scanned":1,"files_failed":1,"rules_skipped":0,"num_violations":1}}"#,
		stderr: b"",
		exit: 0,
	},
];
