//! ltrs — LanguageTool-Rust diagnostics. Ported from the none-ls diagnostics/ltrs builtin.
//!
//! ltrs emits a single JSON object `{ "matches": [ … ] }`. Each match nests its
//! position under `moreContext` and its suggested fixes under `replacements`, and
//! the none-ls builtin composes a custom message (`<message> Try: "a", "b"`) and
//! computes the span from `line_offset`/`length`. The shared `json_diag` helper
//! cannot express that nested/computed shape, so the navigation is hand-written.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "ltrs",
    description: "LanguageTool-Rust (LTRS) is both an executable and a Rust library that aims to provide correct and safe bindings for the LanguageTool API.",
    url: "https://github.com/jeertmans/languagetool-rust",
    operations: &[Operation { mode: "lint", args: &["check", "-r", "{file}"], stdin: false }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let matches = match &value {
		JsonValue::Object(m) => match m.get("matches") {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};
	matches.iter().filter_map(from_match).collect()
}

fn from_match(m: &JsonValue) -> Option<RawDiagnostic> {
	let obj = match m {
		JsonValue::Object(o) => o,
		_ => return None,
	};
	let base_message = get_str(obj.get("message"))?;

	// tip = the replacement values joined as `"a", "b"` (curly quotes upstream).
	let tip = match obj.get("replacements") {
		Some(JsonValue::Array(reps)) => reps
			.iter()
			.filter_map(|r| match r {
				JsonValue::Object(ro) => get_str(ro.get("value")),
				_ => None,
			})
			.map(|v| format!("\u{201c}{v}\u{201d}"))
			.collect::<Vec<_>>()
			.join(", "),
		_ => String::new(),
	};
	let message = format!("{base_message} Try: {tip}");

	let code = obj.get("rule").and_then(|r| match r {
		JsonValue::Object(ro) => get_str(ro.get("id")),
		_ => None,
	});

	let more = match obj.get("moreContext") {
		Some(JsonValue::Object(mc)) => Some(mc),
		_ => None,
	};
	let line = more.and_then(|mc| get_u32(mc.get("line_number")));
	let offset = more.and_then(|mc| get_u32(mc.get("line_offset")));
	let length = get_u32(obj.get("length"));

	// column = line_offset + 1; endColumn = line_offset + length + 1 (upstream).
	let col = offset.map(|o| o + 1);
	let end_col = match (offset, length) {
		(Some(o), Some(l)) => Some(o + l + 1),
		_ => None,
	};

	Some(RawDiagnostic {
		message,
		row: line,
		col,
		end_row: line,
		end_col,
		// Builtin hard-codes level = "ERROR" for every match.
		severity: Some(severity::ERROR),
		code,
		..RawDiagnostic::default()
	})
}

fn get_str(v: Option<&JsonValue>) -> Option<String> {
	match v {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn get_u32(v: Option<&JsonValue>) -> Option<u32> {
	match v {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_match_with_replacements_and_context() {
		let json = br#"{
            "matches": [
                {
                    "message": "Possible spelling mistake found.",
                    "length": 4,
                    "rule": { "id": "MORFOLOGIK_RULE_EN_US" },
                    "replacements": [ { "value": "test" }, { "value": "text" } ],
                    "moreContext": { "line_number": 12, "line_offset": 5 }
                }
            ]
        }"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		let d = &out[0];
		assert_eq!(
			d.message,
			"Possible spelling mistake found. Try: \u{201c}test\u{201d}, \u{201c}text\u{201d}"
		);
		assert_eq!(d.row, Some(12));
		assert_eq!(d.end_row, Some(12));
		assert_eq!(d.col, Some(6));
		assert_eq!(d.end_col, Some(10));
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.code.as_deref(), Some("MORFOLOGIK_RULE_EN_US"));
	}

	#[test]
	fn empty_matches_yields_nothing() {
		assert!(parse(br#"{"matches": []}"#, b"", 0).is_empty());
		assert!(parse(br#"{}"#, b"", 0).is_empty());
	}

	#[test]
	fn match_without_replacements_has_empty_tip() {
		let json = br#"{"matches":[{"message":"X","moreContext":{"line_number":1,"line_offset":0}}]}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "X Try: ");
		assert_eq!(out[0].col, Some(1));
	}
}
