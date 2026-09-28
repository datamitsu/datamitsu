//! ESLint's JSON report: an array of result objects, each with a `filePath` and
//! a `messages` array of findings whose `severity` is numeric (2 error,
//! 1 warning) and whose `ruleId` may be `null` (a parse error). The `eslint`
//! tool parser reads it through here too.
use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::json_diag::{elements, member, text};
use crate::response::Response;
use crate::severity::{self, Level};

/// ESLint's two level numbers, which the `eslint` tool parser shares.
pub(crate) const LEVELS: &[Level] = &[Level("2", severity::ERROR), Level("1", severity::WARNING)];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "eslint-json",
	description: "ESLint's JSON report, `eslint --format json`, and any linter that prints its result array. \
        Recognized by an array whose every element carries `filePath` and a `messages` array, findings \
        or not.",
	url: "https://eslint.org/docs/latest/use/formatters/#json",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	super::each_stream(DESCRIPTOR.name, stdout, stderr, |s| {
		crate::json_diag::find_envelope(s, envelope)
	})
}

/// The findings of an ESLint report in `stream`, read leniently: whatever
/// document in it yields the most, as the `eslint` tool parser always has.
#[cfg(feature = "tools")]
pub(crate) fn findings(stream: &[u8]) -> Vec<RawDiagnostic> {
	crate::json_diag::extract_lenient(stream, from_report)
}

fn envelope(report: &JsonValue) -> Option<Vec<RawDiagnostic>> {
	let results = elements(report).filter(|a| !a.is_empty())?;
	let is_result = |r: &JsonValue| {
		member(r, "filePath").and_then(text).is_some() && member(r, "messages").and_then(elements).is_some()
	};
	results.iter().all(is_result).then(|| from_report(report))
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
		Some(JsonValue::Number(n)) => crate::numconv::json_int(*n).and_then(|n| severity::of(LEVELS, &n.to_string())),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn a_cut_off_report_is_read_as_no_format() {
		let cut = br#"[{"filePath":"/a.js","messages":[{"ruleId":"r","severity":2,"message":"m","line":1,"column":1}]"#;
		assert!(!parse(cut, b"", 1).recognized);
		assert!(!crate::fallback::sniff(cut, b"", 1).recognized);
	}

	pub(super) const REPORT: &[u8] = br#"[{"filePath":"/x/a.js","messages":[{"ruleId":"semi","severity":1,"message":"Missing semicolon.","line":1,"column":10,"endLine":1,"endColumn":11}]},{"filePath":"/x/b.js","messages":[]}]"#;

	#[test]
	fn reads_a_report() {
		let r = parse(REPORT, b"", 1);
		assert!(r.recognized);
		assert_eq!(r.diagnostics.len(), 1);
		assert_eq!(r.diagnostics[0].file.as_deref(), Some("/x/a.js"));
		assert_eq!(r.diagnostics[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn a_clean_report_is_recognized_and_empty() {
		let r = parse(br#"[{"filePath":"/x/ok.js","messages":[]}]"#, b"", 0);
		assert!(r.recognized && r.diagnostics.is_empty());
	}

	#[test]
	fn an_array_of_something_else_is_no_report() {
		for out in [
			&b"[]"[..],
			br#"[{"filePath":"a"}]"#,
			br#"[{"message":"x","line":1}]"#,
			br#"[{"filePath":"a","messages":[]}"#,
			b"{}",
		] {
			assert!(!parse(out, b"", 1).recognized, "{}", String::from_utf8_lossy(out));
		}
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: tests::REPORT,
	stderr: b"",
	exit: 1,
}];
