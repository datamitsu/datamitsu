//! textlint — The pluggable linting tool for text and Markdown. Ported from the
//! none-ls diagnostics/textlint builtin.
//!
//! textlint `-f json` emits an array of per-file results, each shaped like
//! `{"filePath": "...", "messages": [...]}`. The builtin reads `output[1].messages`
//! (the first file's messages) and maps each message object with the default JSON
//! attributes plus `severity` as a numeric token: per the builtin's `severities`
//! table the values map `1 -> warning`, `2 -> error`. textlint messages carry
//! `line`, `column`, `ruleId` and `message`; it emits no end span, so those stay
//! unset.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;
use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "textlint",
	description: "The pluggable linting tool for text and Markdown.",
	url: "https://github.com/textlint/textlint",
	operations: &[Operation {
		mode: "lint",
		args: &["-f", "json", "--stdin", "--stdin-filename", "{file}"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	// The builtin uses only the first file's messages (output[1].messages).
	let messages = match &value {
		JsonValue::Array(files) => files
			.first()
			.and_then(|f| match f {
				JsonValue::Object(m) => m.get("messages"),
				_ => None,
			})
			.and_then(|m| match m {
				JsonValue::Array(items) => Some(items),
				_ => None,
			}),
		_ => None,
	};
	let Some(messages) = messages else {
		return Vec::new();
	};

	// Default none-ls attributes (line/column/ruleId/message); severity below is
	// numeric, so it is mapped separately rather than via the string SeverityMap.
	let attrs = Attrs::defaults();
	let mut out = Vec::new();
	for msg in messages {
		let Some(mut d) = json_diag::from_obj(msg, &attrs, |_| None) else {
			continue;
		};
		if let JsonValue::Object(m) = msg {
			if let Some(JsonValue::Number(n)) = m.get("severity") {
				if let Some(v) = crate::numconv::json_int(*n) {
					d.severity = severity_of(v);
				}
			}
		}
		out.push(d);
	}
	out
}

/// textlint's numeric severity: 1 = warning, 2 = error (builtin `severities` order).
fn severity_of(level: i64) -> Option<u8> {
	match level {
		1 => Some(severity::WARNING),
		2 => Some(severity::ERROR),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_textlint_json() {
		let json = br#"[
            {
                "filePath": "doc.md",
                "messages": [
                    {"ruleId":"no-todo","message":"Found TODO: 'X'","line":3,"column":5,"severity":2},
                    {"ruleId":"ja-no-mixed-period","message":"Missing period.","line":10,"column":1,"severity":1}
                ]
            }
        ]"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Found TODO: 'X'");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].code.as_deref(), Some("no-todo"));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].end_row, None);
		assert_eq!(out[1].severity, Some(severity::WARNING));
	}

	#[test]
	fn empty_messages_yields_nothing() {
		let json = br#"[{"filePath":"doc.md","messages":[]}]"#;
		assert!(parse(json, b"", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 1).is_empty());
	}
}
