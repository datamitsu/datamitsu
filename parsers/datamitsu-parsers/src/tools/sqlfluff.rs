//! sqlfluff — A SQL linter and auto-formatter for Humans. Ported from the
//! none-ls diagnostics/sqlfluff builtin.
//!
//! sqlfluff is run with `-f github-annotation`, which emits a JSON array of
//! GitHub annotation objects: `start_line`/`start_column`/`end_line`/
//! `end_column` (1-based, the end exclusive), `annotation_level` (one of
//! GitHub's `notice`/`warning`/`failure`) and `message`, which sqlfluff writes
//! as `<rule>: <description>` — the rule becomes the code.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "sqlfluff",
	description: "A SQL linter and auto-formatter for Humans",
	url: "https://github.com/sqlfluff/sqlfluff",
	severities: &[
		Level("failure", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("notice", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"lint",
			"--disable-progress-bar",
			"-f",
			"github-annotation",
			"-n",
			"{file}",
		],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let attrs = Attrs {
		row: "start_line",
		col: "start_column",
		end_row: "end_line",
		end_col: "end_column",
		severity: "annotation_level",
		message: "message",
		..Attrs::defaults()
	};
	let mut out = json_diag::from_json(stdout, &attrs, severity_of);
	for d in &mut out {
		if let Some((code, description)) = split_rule(&d.message) {
			d.code = Some(code.to_string());
			d.message = description.to_string();
		}
	}
	out
}

fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
}

/// Split `<rule>: <description>`; `None` when the message has no rule prefix.
fn split_rule(message: &str) -> Option<(&str, &str)> {
	let (code, description) = message.split_once(": ")?;
	let is_rule = !code.is_empty() && code.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_');
	is_rule.then_some((code, description))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_github_annotation_array() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_row, Some(1));
		assert_eq!(out[0].end_col, Some(7));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].code.as_deref(), Some("CP01"));
		assert_eq!(out[0].message, "Keywords must be consistently upper case.");
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].code.as_deref(), Some("LT02"));
	}

	#[test]
	fn maps_notice_to_info() {
		let json = br#"[{"start_line":2,"start_column":1,"annotation_level":"notice","message":"hi"}]"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, Some(severity::INFO));
		assert_eq!(out[0].code, None);
		assert_eq!(out[0].message, "hi");
	}

	#[test]
	fn an_unknown_annotation_level_has_none() {
		let json = br#"[{"start_line":2,"start_column":1,"annotation_level":"error","message":"LT01: x"}]"#;
		assert_eq!(parse(json, b"", 1)[0].severity, None);
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"[{"file": "a.sql", "start_line": 1, "start_column": 1, "end_line": 1, "end_column": 7, "title": "SQLFluff", "message": "CP01: Keywords must be consistently upper case.", "annotation_level": "failure"}, {"file": "a.sql", "start_line": 3, "start_column": 2, "end_line": 3, "end_column": 2, "title": "SQLFluff", "message": "LT02: Expected indent of 4 spaces.", "annotation_level": "warning"}]"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"[{"file": "b.sql", "start_line": 4, "start_column": 8, "end_line": 5, "end_column": 3, "title": "SQLFluff", "message": "RF02: Unqualified reference 'id' found in select with more than one referenced table.", "annotation_level": "notice"}]"#,
		stderr: b"",
		exit: 1,
	},
];
