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
//! `range` is LSP-shaped: 0-based lines and characters with an exclusive end,
//! so 1 is added to all four. `severity` is a number, 0 (error) … 3 (hint);
//! `code` is the rule (string or number).
//!
//! `path` is the JSON path inside the document, not a file, and is dropped;
//! `source` is the document the result is in, which becomes the file.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "spectral",
	description: "A flexible JSON/YAML linter for creating automated style guides, with baked in support for OpenAPI v3.1, v3.0, and v2.0.",
	url: "https://github.com/stoplightio/spectral",
	severities: &[
		Level("0", severity::ERROR),
		Level("1", severity::WARNING),
		Level("2", severity::INFO),
		Level("3", severity::HINT),
	],
	column_unit: "",
	category: "",
	kind: "tool",
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

	let severity = obj
		.get("severity")
		.and_then(number_u32)
		.and_then(|n| severity::of(DESCRIPTOR.severities, &n.to_string()));

	let code = obj.get("code").and_then(|c| match c {
		JsonValue::String(s) => Some(s.clone()),
		JsonValue::Number(n) => Some(format_int(*n)),
		_ => None,
	});

	let one_based = |v: Option<u32>| v.and_then(|v| v.checked_add(1));
	Some(RawDiagnostic {
		message,
		row: one_based(row),
		col: one_based(col),
		end_row: one_based(end_row),
		end_col: one_based(end_col),
		severity,
		source: Some("Spectral".to_string()),
		code,
		file: match obj.get("source") {
			Some(JsonValue::String(s)) => crate::diagnostic::file_field(s),
			_ => None,
		},
		..RawDiagnostic::default()
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
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "Object must have required property.");
		assert_eq!(out[0].row, Some(4));
		assert_eq!(out[0].col, Some(6));
		assert_eq!(out[0].end_row, Some(4));
		assert_eq!(out[0].end_col, Some(13));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("Spectral"));
		assert_eq!(out[0].code.as_deref(), Some("oas3-schema"));

		assert_eq!(out[1].severity, Some(severity::INFO));
		assert_eq!(out[1].code.as_deref(), Some("42"));
		assert_eq!(out[1].row, Some(10));
		assert_eq!((out[1].col, out[1].end_col), (Some(1), Some(5)));
	}

	#[test]
	fn reads_every_printed_level() {
		let out = parse(SAMPLES[1].stdout, b"", 0);
		let levels: Vec<_> = out.iter().map(|d| d.severity).collect();
		assert_eq!(levels, [Some(severity::WARNING), Some(severity::HINT)]);
	}

	#[test]
	fn an_off_scale_severity_has_no_level() {
		let json = br#"[{"code":"c","message":"m","severity":7}]"#;
		assert_eq!(parse(json, b"", 0)[0].severity, None);
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
	#[test]
	fn a_position_without_a_successor_is_dropped() {
		let json = br#"[{"code":"c","message":"m","severity":0,"range":{"start":{"line":4294967295,"character":4294967295},"end":{"line":0,"character":0}}}]"#;
		let out = parse(json, b"", 1);
		assert_eq!((out[0].row, out[0].col), (None, None));
	}

	#[test]
	fn each_finding_names_its_source_document() {
		let json = br#"[
            {"code":"a","message":"first","severity":0,"source":"/w/openapi.yaml"},
            {"code":"b","message":"second","severity":1,"source":"/w/schemas/pet.yaml"}]"#;
		let out = parse(json, b"", 1);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(
			got,
			[
				("first", Some("/w/openapi.yaml")),
				("second", Some("/w/schemas/pet.yaml"))
			]
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"[
            {"code":"oas3-schema","path":["paths","/pets","get"],"message":"Object must have required property.",
             "severity":0,"source":"openapi.yaml",
             "range":{"start":{"line":3,"character":5},"end":{"line":3,"character":12}}},
            {"code":42,"path":["info"],"message":"Info-level note.",
             "severity":2,"source":"openapi.yaml",
             "range":{"start":{"line":9,"character":0},"end":{"line":9,"character":4}}}
        ]"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"[
            {"code":"operation-description","path":["paths","/pets","get"],"message":"Operation \"description\" must be present and non-empty string.",
             "severity":1,"source":"openapi.yaml",
             "range":{"start":{"line":6,"character":8},"end":{"line":14,"character":29}}},
            {"code":"info-contact","path":["info"],"message":"Info object must have \"contact\" object.",
             "severity":3,"source":"openapi.yaml",
             "range":{"start":{"line":1,"character":5},"end":{"line":3,"character":16}}}
        ]"#,
		stderr: b"",
		exit: 0,
	},
];
