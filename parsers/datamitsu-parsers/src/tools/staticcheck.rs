//! staticcheck — Advanced Go linter. Ported from the none-ls diagnostics/staticcheck builtin.
//!
//! `staticcheck -f json ./...` emits NDJSON: one JSON object per line, e.g.
//! `{"code":"ST1003","severity":"error","location":{"file":"a.go","line":3,"column":5},
//!   "end":{"file":"a.go","line":3,"column":9},"message":"..."}`.
//! none-ls (`format = "line"`) decodes each line and reads `location.line` /
//! `location.column`, `end.line`, `code`, `message`, and maps `severity`
//! (error/warning/ignored).
//!
//! Faithful quirk: upstream reads `decoded["end"]["culumn"]` (typo) for end_col,
//! a key staticcheck never emits, so end_col is always absent. We mirror that —
//! end_col stays None.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "staticcheck",
	description: "Advanced Go linter.",
	url: "https://staticcheck.io/",
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

	let (row, col) = match map.get("location") {
		Some(JsonValue::Object(loc)) => (get_u32(loc, "line"), get_u32(loc, "column")),
		_ => (None, None),
	};

	// Upstream reads end.line (and the misspelled end.culumn, hence no end_col).
	let end_row = match map.get("end") {
		Some(JsonValue::Object(end)) => get_u32(end, "line"),
		_ => None,
	};

	let severity = match map.get("severity") {
		Some(JsonValue::String(s)) => severity_of(s),
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
		severity,
		source: Some("staticcheck".to_string()),
		code,
		..RawDiagnostic::default()
	})
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		"ignored" => Some(severity::INFO),
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
	fn parses_ndjson_lines() {
		let stdout = br#"{"code":"ST1003","severity":"error","location":{"file":"a.go","line":3,"column":5},"end":{"file":"a.go","line":3,"column":9},"message":"should not use underscores in Go names"}
{"code":"U1000","severity":"warning","location":{"file":"b.go","line":10,"column":2},"end":{"file":"b.go","line":10,"column":7},"message":"func unused is unused"}"#;
		let out = parse(stdout, b"", 1);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "should not use underscores in Go names");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_row, Some(3));
		assert_eq!(out[0].end_col, None); // upstream typo: end.culumn never present
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].code.as_deref(), Some("ST1003"));
		assert_eq!(out[0].source.as_deref(), Some("staticcheck"));

		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].code.as_deref(), Some("U1000"));
		assert_eq!(out[1].row, Some(10));
	}

	#[test]
	fn ignored_maps_to_info_and_blank_lines_skipped() {
		let stdout = br#"
{"code":"S1000","severity":"ignored","location":{"file":"c.go","line":1,"column":1},"message":"redundant"}
"#;
		let out = parse(stdout, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, Some(severity::INFO));
	}
}
