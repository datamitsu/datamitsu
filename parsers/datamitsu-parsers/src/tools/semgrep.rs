//! semgrep — static analysis for bugs and code standards. Ported from the
//! none-ls diagnostics/semgrep builtin.
//!
//! Semgrep's `--json` output nests diagnostics under `results[]`, with the
//! message/severity inside `extra` and the span split across `start`/`end`
//! objects, so this parser walks the structure directly (the flat
//! `json_diag::from_json` mapper can't reach the nested fields). Field mapping
//! mirrors the builtin's `handle_semgrep_output`: message=extra.message,
//! ruleId=check_id, level=extra.severity, line/column=start.line/start.col,
//! endLine/endColumn=end.line/end.col (1-based, the end exclusive). The rule's
//! `extra.metadata.source` is its documentation page (semgrep's SARIF `helpUri`).

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "semgrep",
	description: "Semgrep is a fast, open-source, static analysis tool for finding bugs and \
        enforcing code standards at editor, commit, and CI time.",
	url: "https://semgrep.dev/",
	severities: &[
		Level("ERROR", severity::ERROR),
		Level("WARNING", severity::WARNING),
		Level("INFO", severity::INFO),
		Level("CRITICAL", severity::ERROR),
		Level("HIGH", severity::ERROR),
		Level("MEDIUM", severity::WARNING),
		Level("LOW", severity::INFO),
	],
	column_unit: "",
	category: "security",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-q", "--json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let results = match &value {
		JsonValue::Object(m) => match m.get("results") {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};
	results.iter().filter_map(from_result).collect()
}

fn from_result(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let extra = obj(map.get("extra"));
	let message = extra.and_then(|e| get_str(e, "message"))?;
	let start = obj(map.get("start"));
	let end = obj(map.get("end"));
	Some(RawDiagnostic {
		message,
		code: get_str(map, "check_id"),
		severity: extra
			.and_then(|e| get_str(e, "severity"))
			.and_then(|s| severity::of(DESCRIPTOR.severities, &s)),
		url: extra
			.and_then(|e| obj(e.get("metadata")))
			.and_then(|m| get_str(m, "source"))
			.filter(|u| u.starts_with("https://") || u.starts_with("http://")),
		row: start.and_then(|s| get_u32(s, "line")),
		col: start.and_then(|s| get_u32(s, "col")),
		end_row: end.and_then(|e| get_u32(e, "line")),
		end_col: end.and_then(|e| get_u32(e, "col")),
		..RawDiagnostic::default()
	})
}

fn obj(v: Option<&JsonValue>) -> Option<&HashMap<String, JsonValue>> {
	match v {
		Some(JsonValue::Object(m)) => Some(m),
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
	fn parses_nested_result() {
		let out = parse(SAMPLES[0].stdout, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Found dangerous system call");
		assert_eq!(
			out[0].code.as_deref(),
			Some("python.lang.security.audit.dangerous-system-call")
		);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://semgrep.dev/r/python.lang.security.audit.dangerous-system-call")
		);
		assert_eq!(out[0].row, Some(10));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_row, Some(10));
		assert_eq!(out[0].end_col, Some(30));
		assert_eq!(out[0].source, None);
	}

	#[test]
	fn reads_every_printed_level() {
		let out = parse(SAMPLES[1].stdout, b"", 1);
		let levels: Vec<_> = out.iter().map(|d| d.severity).collect();
		assert_eq!(
			levels,
			[
				Some(severity::WARNING),
				Some(severity::INFO),
				Some(severity::ERROR),
				Some(severity::ERROR),
				Some(severity::WARNING),
				Some(severity::INFO),
				None,
			]
		);
	}

	#[test]
	fn a_rule_without_a_source_url_has_none() {
		let out = parse(SAMPLES[1].stdout, b"", 1);
		assert_eq!(out[1].url, None);
		assert_eq!(out[6].url, None);
	}

	#[test]
	fn no_results_yields_nothing() {
		assert!(parse(br#"{"results": []}"#, b"", 0).is_empty());
		assert!(parse(b"not json", b"", 1).is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{
            "version": "1.85.0",
            "results": [
                {
                    "check_id": "python.lang.security.audit.dangerous-system-call",
                    "path": "app.py",
                    "start": {"line": 10, "col": 5, "offset": 180},
                    "end": {"line": 10, "col": 30, "offset": 205},
                    "extra": {
                        "message": "Found dangerous system call",
                        "metadata": {
                            "category": "security",
                            "source": "https://semgrep.dev/r/python.lang.security.audit.dangerous-system-call",
                            "shortlink": "https://sg.run/abcd"
                        },
                        "severity": "ERROR",
                        "fingerprint": "requires login",
                        "lines": "requires login",
                        "validation_state": "NO_VALIDATOR",
                        "engine_kind": "OSS"
                    }
                }
            ],
            "errors": [],
            "paths": {"scanned": ["app.py"]}
        }"#,
		stderr: b"",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: br#"{"version":"1.85.0","results":[
            {"check_id":"r.warning","path":"a.py","start":{"line":1,"col":1,"offset":0},"end":{"line":1,"col":5,"offset":4},
             "extra":{"message":"m1","metadata":{"source":"https://semgrep.dev/r/r.warning"},"severity":"WARNING","lines":"requires login"}},
            {"check_id":"r.info","path":"a.py","start":{"line":2,"col":1,"offset":5},"end":{"line":3,"col":2,"offset":20},
             "extra":{"message":"m2","metadata":{},"severity":"INFO","lines":"requires login"}},
            {"check_id":"r.critical","path":"a.py","start":{"line":4,"col":1,"offset":21},"end":{"line":4,"col":9,"offset":29},
             "extra":{"message":"m3","metadata":{"source":"https://semgrep.dev/r/r.critical"},"severity":"CRITICAL","lines":"requires login"}},
            {"check_id":"r.high","path":"a.py","start":{"line":5,"col":1,"offset":30},"end":{"line":5,"col":9,"offset":38},
             "extra":{"message":"m4","metadata":{"source":"https://semgrep.dev/r/r.high"},"severity":"HIGH","lines":"requires login"}},
            {"check_id":"r.medium","path":"a.py","start":{"line":6,"col":1,"offset":39},"end":{"line":6,"col":9,"offset":47},
             "extra":{"message":"m5","metadata":{"source":"https://semgrep.dev/r/r.medium"},"severity":"MEDIUM","lines":"requires login"}},
            {"check_id":"r.low","path":"a.py","start":{"line":7,"col":1,"offset":48},"end":{"line":7,"col":9,"offset":56},
             "extra":{"message":"m6","metadata":{"source":"https://semgrep.dev/r/r.low"},"severity":"LOW","lines":"requires login"}},
            {"check_id":"r.inventory","path":"a.py","start":{"line":8,"col":1,"offset":57},"end":{"line":8,"col":9,"offset":65},
             "extra":{"message":"m7","metadata":{"source":"local rules"},"severity":"INVENTORY","lines":"requires login"}}
        ],"errors":[],"paths":{"scanned":["a.py"]}}"#,
		stderr: b"",
		exit: 1,
	},
];
