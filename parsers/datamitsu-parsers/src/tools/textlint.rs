//! textlint — The pluggable linting tool for text and Markdown. Ported from the
//! none-ls diagnostics/textlint builtin.
//!
//! textlint `-f json` emits an array of per-file results, each shaped like
//! `{"filePath": "...", "messages": [...]}`. The builtin reads `output[1].messages`
//! (the first file's messages) and maps each message object with the default JSON
//! attributes plus `severity` as a numeric token: textlint's severity levels are
//! `1 -> warning`, `2 -> error`, `3 -> info`. textlint messages carry 1-based
//! `line` and `column`, `ruleId` and `message`; the end span is not read, so it
//! stays unset.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};
use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "textlint",
	description: "The pluggable linting tool for text and Markdown.",
	url: "https://github.com/textlint/textlint",
	severities: &[
		Level("1", severity::WARNING),
		Level("2", severity::ERROR),
		Level("3", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
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

fn severity_of(level: i64) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, &level.to_string())
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
	fn reads_info_and_leaves_an_unlisted_level_unset() {
		let json = br#"[{"filePath":"doc.md","messages":[
            {"ruleId":"a","message":"m","line":1,"column":1,"severity":3},
            {"ruleId":"b","message":"m","line":2,"column":1,"severity":0}
        ]}]"#;
		let out = parse(json, b"", 0);
		assert_eq!(out[0].severity, Some(severity::INFO));
		assert_eq!(out[1].severity, None);
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

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"[{"messages":[{"type":"lint","ruleId":"no-todo","message":"Found TODO: '- [ ] write the intro'","index":2,"line":3,"column":5,"range":[2,6],"loc":{"start":{"line":3,"column":5},"end":{"line":3,"column":9}},"severity":2},{"type":"lint","ruleId":"sentence-length","message":"Line 10 sentence length(112) exceeds the maximum sentence length of 100.","index":40,"line":10,"column":1,"range":[40,152],"loc":{"start":{"line":10,"column":1},"end":{"line":10,"column":113}},"severity":1}],"filePath":"/work/textlint/doc.md"}]"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"[{"messages":[{"type":"lint","ruleId":"terminology","message":"Incorrect term: 'javascript', use 'JavaScript' instead","index":0,"line":1,"column":1,"range":[0,10],"loc":{"start":{"line":1,"column":1},"end":{"line":1,"column":11}},"severity":3}],"filePath":"/work/textlint/doc.md"}]"#,
		stderr: b"",
		exit: 0,
	},
];
