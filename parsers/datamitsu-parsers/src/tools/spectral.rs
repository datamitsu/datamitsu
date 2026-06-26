//! spectral — flexible JSON/YAML linter with baked-in OpenAPI support.
//!
//! Ported from the none-ls `diagnostics/spectral` builtin. Spectral runs with
//! `-f json` and emits a top-level array of result objects:
//!
//! ```json
//! [{"code":"oas3-schema","message":"...","severity":0,
//!   "range":{"start":{"line":3,"character":5},"end":{"line":3,"character":12}}}]
//! ```
//!
//! The builtin's `on_output` maps each result faithfully:
//!   row     = range.start.line + 1   (Spectral lines are 0-based)
//!   col     = range.start.character  (passed through, 0-based)
//!   end_row = range.end.line + 1
//!   end_col = range.end.character    (passed through, 0-based)
//!   source  = "Spectral"
//!   severity= severities[d.severity + 1] over {ERROR, WARNING, INFO, HINT}
//!             — Spectral severities are 0-based (0=error … 3=hint).
//!   code    = d.code (string or number)
//!
//! The builtin also carries `path`, but RawDiagnostic has no path field, so it is
//! dropped (the Go core fills positional context).
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "spectral",
    description: "A flexible JSON/YAML linter for creating automated style guides, with baked in support for OpenAPI v3.1, v3.0, and v2.0.",
    url: "https://github.com/stoplightio/spectral",
    operations: &[Operation {
        mode: "lint",
        args: &["lint", "--stdin-filepath", "{file}", "-f", "json"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let items = match &value {
		JsonValue::Array(items) => items,
		_ => return Vec::new(),
	};
	items.iter().filter_map(parse_result).collect()
}

fn parse_result(item: &JsonValue) -> Option<RawDiagnostic> {
	let obj = match item {
		JsonValue::Object(map) => map,
		_ => return None,
	};

	// message is the only mandatory field.
	let message = match obj.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};

	let range = obj.get("range").and_then(|r| match r {
		JsonValue::Object(m) => Some(m),
		_ => None,
	});
	let (row, col) = range.and_then(|m| m.get("start")).map(point).unwrap_or((None, None));
	let (end_row, end_col) = range.and_then(|m| m.get("end")).map(point).unwrap_or((None, None));

	let severity = obj.get("severity").and_then(number_u32).and_then(severity_of);

	let code = obj.get("code").and_then(|c| match c {
		JsonValue::String(s) => Some(s.clone()),
		JsonValue::Number(n) => Some(format_int(*n)),
		_ => None,
	});

	Some(RawDiagnostic {
		message,
		// Lines are 0-based in Spectral; the builtin adds 1. Characters pass
		// through unchanged (0-based).
		row: row.map(|r| r + 1),
		col,
		end_row: end_row.map(|r| r + 1),
		end_col,
		severity,
		source: Some("Spectral".to_string()),
		code,
	})
}

/// Extract `{line, character}` from a range endpoint object.
fn point(p: &JsonValue) -> (Option<u32>, Option<u32>) {
	let m = match p {
		JsonValue::Object(m) => m,
		_ => return (None, None),
	};
	let line = m.get("line").and_then(number_u32);
	let character = m.get("character").and_then(number_u32);
	(line, character)
}

/// Map Spectral's 0-based severity (0=error … 3=hint) onto the shared scale,
/// matching the builtin's `severities[d.severity + 1]` table lookup.
fn severity_of(level: u32) -> Option<u8> {
	match level {
		0 => Some(severity::ERROR),
		1 => Some(severity::WARNING),
		2 => Some(severity::INFO),
		3 => Some(severity::HINT),
		_ => None,
	}
}

fn number_u32(v: &JsonValue) -> Option<u32> {
	match v {
		JsonValue::Number(n) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

fn format_int(n: f64) -> String {
	if n.fract() == 0.0 {
		format!("{}", n as i64)
	} else {
		format!("{n}")
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_results() {
		let json = br#"[
            {"code":"oas3-schema","message":"Object must have required property.",
             "severity":0,
             "range":{"start":{"line":3,"character":5},"end":{"line":3,"character":12}}},
            {"code":42,"message":"Info-level note.",
             "severity":2,
             "range":{"start":{"line":9,"character":0},"end":{"line":9,"character":4}}}
        ]"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "Object must have required property.");
		assert_eq!(out[0].row, Some(4)); // 3 + 1
		assert_eq!(out[0].col, Some(5)); // passthrough
		assert_eq!(out[0].end_row, Some(4));
		assert_eq!(out[0].end_col, Some(12));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("Spectral"));
		assert_eq!(out[0].code.as_deref(), Some("oas3-schema"));

		assert_eq!(out[1].severity, Some(severity::INFO));
		assert_eq!(out[1].code.as_deref(), Some("42"));
		assert_eq!(out[1].row, Some(10));
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}

	#[test]
	fn ignores_non_array_or_garbage() {
		assert!(parse(b"not json", b"", 0).is_empty());
		assert!(parse(br#"{"message":"x"}"#, b"", 0).is_empty());
	}
}
