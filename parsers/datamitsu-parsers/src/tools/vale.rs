//! vale — syntax-aware prose linter. Ported from the none-ls diagnostics/vale builtin.
//!
//! Vale's JSON output (`--output JSON`) is not a flat array: it is an object
//! keyed by filename (or `stdin.<ext>` when reading stdin), each value an array
//! of diagnostic objects with PascalCase fields — `Line`, `Span` ([start, end]),
//! `Check`, `Message`, `Severity`. This shape (filename map + the `Span` pair)
//! does not fit `json_diag::from_json`, so the mapping is hand-rolled over
//! tinyjson. Faithful to the builtin: row=Line, col=Span[1], end_col=Span[2]+1,
//! code=Check, message=Message, severity from {error, warning, suggestion}.
use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "vale",
	description: "Syntax-aware linter for prose built with speed and extensibility in mind.",
	url: "https://vale.sh/",
	// to_stdin=true; vale reads the buffer on stdin. `--ext` is per-file in the
	// builtin (it derives the extension from the bufname); a representative
	// markdown extension stands in for the static recipe.
	operations: &[Operation {
		mode: "lint",
		args: &["--no-exit", "--output", "JSON", "--ext", ".md"],
		stdin: true,
	}],
};

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		"suggestion" => Some(severity::HINT),
		_ => None,
	}
}

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let mut out = Vec::new();
	// Top level is an object keyed by filename; iterate every file's array.
	if let JsonValue::Object(files) = &value {
		for items in files.values() {
			if let JsonValue::Array(arr) = items {
				for it in arr {
					if let JsonValue::Object(obj) = it {
						if let Some(d) = from_obj(obj) {
							out.push(d);
						}
					}
				}
			}
		}
	}
	out
}

fn from_obj(obj: &HashMap<String, JsonValue>) -> Option<RawDiagnostic> {
	let message = match obj.get("Message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	let (col, end_col) = match obj.get("Span") {
		Some(JsonValue::Array(span)) => {
			let start = span.first().and_then(as_u32);
			// end_col = Span[2] + 1 in the builtin (1-based, inclusive→exclusive).
			let end = span.get(1).and_then(as_u32).map(|e| e + 1);
			(start, end)
		}
		_ => (None, None),
	};
	let severity = match obj.get("Severity") {
		Some(JsonValue::String(s)) => severity_of(s),
		_ => None,
	};
	Some(RawDiagnostic {
		message,
		row: obj.get("Line").and_then(as_u32),
		col,
		end_col,
		code: match obj.get("Check") {
			Some(JsonValue::String(s)) => Some(s.clone()),
			_ => None,
		},
		severity,
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
	fn parses_vale_json_keyed_by_filename() {
		let json = br#"{
            "stdin.md": [
                {
                    "Check": "Vale.Spelling",
                    "Line": 4,
                    "Message": "Did you really mean 'mispeled'?",
                    "Severity": "error",
                    "Span": [10, 18]
                },
                {
                    "Check": "write-good.Weasel",
                    "Line": 7,
                    "Message": "'very' is a weasel word!",
                    "Severity": "suggestion",
                    "Span": [1, 4]
                }
            ]
        }"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "Did you really mean 'mispeled'?");
		assert_eq!(out[0].row, Some(4));
		assert_eq!(out[0].col, Some(10));
		assert_eq!(out[0].end_col, Some(19)); // Span[2] + 1
		assert_eq!(out[0].code.as_deref(), Some("Vale.Spelling"));
		assert_eq!(out[0].severity, Some(severity::ERROR));

		assert_eq!(out[1].severity, Some(severity::HINT));
		assert_eq!(out[1].col, Some(1));
		assert_eq!(out[1].end_col, Some(5));
	}

	#[test]
	fn empty_object_yields_nothing() {
		assert!(parse(b"{}", b"", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 1).is_empty());
	}
}
