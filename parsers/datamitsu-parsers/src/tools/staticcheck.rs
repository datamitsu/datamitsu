//! staticcheck — Advanced Go linter. Ported from the none-ls diagnostics/staticcheck builtin.
//!
//! `staticcheck -f json ./...` emits NDJSON: one JSON object per line, e.g.
//! `{"code":"ST1003","severity":"error","location":{"file":"a.go","line":3,"column":5},
//!   "end":{"file":"a.go","line":3,"column":9},"message":"..."}`.
//! Positions are go/token ones: 1-based, with `end` just past the span
//! (exclusive). A diagnostic without a range prints a zero `end`, which is no end.
//! `severity` is `error` or `warning`; `ignored`, printed only under
//! `-show-ignored`, marks a problem a directive suppressed, which is no finding.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "staticcheck",
	description: "Advanced Go linter.",
	url: "https://staticcheck.io/",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	// to_stdin = false, multiple_files = true: staticcheck runs against the package
	// tree (`./...`), not a single file or stdin. No {file}/stdin placeholder.
	operations: &[Operation {
		mode: "lint",
		args: &["-f", "json", "./..."],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stdout).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let line = line.trim();
	if line.is_empty() {
		return None;
	}
	let value: JsonValue = line.parse().ok()?;
	let map = match &value {
		JsonValue::Object(m) => m,
		_ => return None,
	};

	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};

	let (row, col) = position(map.get("location"));
	let file = match map.get("location") {
		Some(JsonValue::Object(l)) => match l.get("file") {
			Some(JsonValue::String(f)) => crate::diagnostic::file_field(f),
			_ => None,
		},
		_ => None,
	};
	let (end_row, end_col) = position(map.get("end"));

	let severity = match map.get("severity") {
		Some(JsonValue::String(s)) if s == "ignored" => return None,
		Some(JsonValue::String(s)) => severity::of(DESCRIPTOR.severities, s),
		_ => None,
	};

	let code = match map.get("code") {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	};

	Some(RawDiagnostic {
		message,
		row,
		col,
		end_row,
		end_col,
		severity,
		source: Some("staticcheck".to_string()),
		code,
		file,
		..RawDiagnostic::default()
	})
}

/// The `line` and `column` of a location object; go/token's zero value (line
/// 0) is an absent position.
fn position(value: Option<&JsonValue>) -> (Option<u32>, Option<u32>) {
	match value {
		Some(JsonValue::Object(loc)) => {
			let line = get_u32(loc, "line").filter(|&l| l > 0);
			let column = line.and(get_u32(loc, "column")).filter(|&c| c > 0);
			(line, column)
		}
		_ => (None, None),
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
	fn parses_ndjson_lines() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 2);

		assert_eq!(
			out[0].message,
			"should not use underscores in Go names; var foo_bar should be fooBar"
		);
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_row, Some(3));
		assert_eq!(out[0].end_col, Some(12));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].code.as_deref(), Some("ST1003"));
		assert_eq!(out[0].source.as_deref(), Some("staticcheck"));

		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].code.as_deref(), Some("U1000"));
		assert_eq!(out[1].row, Some(10));
	}

	#[test]
	fn a_zero_end_is_no_end() {
		let out = parse(SAMPLES[1].stdout, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!((out[0].row, out[0].col), (Some(1), Some(1)));
		assert_eq!((out[0].end_row, out[0].end_col), (None, None));
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn a_suppressed_problem_is_no_finding() {
		let stdout =
			br#"{"code":"S1000","severity":"ignored","location":{"file":"c.go","line":1,"column":1},"message":"suppressed"}
{"code":"S1001","severity":"warning","location":{"file":"c.go","line":2,"column":1},"message":"kept"}
"#;
		let out = parse(stdout, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].code.as_deref(), Some("S1001"));
	}

	#[test]
	fn an_unknown_severity_has_no_level_and_blank_lines_are_skipped() {
		let stdout = br#"
{"code":"S1000","severity":"note","location":{"file":"c.go","line":1,"column":1},"message":"redundant"}
"#;
		let out = parse(stdout, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, None);
	}
	#[test]
	fn names_the_file_of_each_finding() {
		let json = br#"{"code":"U1000","severity":"warning","location":{"file":"/src/b.go","line":10,"column":6},"message":"func unused is unused"}"#;
		assert_eq!(parse(json, b"", 1)[0].file.as_deref(), Some("/src/b.go"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{"code":"ST1003","severity":"error","location":{"file":"/src/a.go","line":3,"column":5},"end":{"file":"/src/a.go","line":3,"column":12},"message":"should not use underscores in Go names; var foo_bar should be fooBar"}
{"code":"U1000","severity":"warning","location":{"file":"/src/b.go","line":10,"column":6},"end":{"file":"/src/b.go","line":10,"column":12},"message":"func unused is unused"}
"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"{"code":"S1000","severity":"warning","location":{"file":"/src/c.go","line":1,"column":1},"end":{"file":"","line":0,"column":0},"message":"should use for range instead of for { select {} }"}
"#,
		stderr: b"",
		exit: 0,
	},
];
