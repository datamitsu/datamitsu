//! sqlfluff — A SQL linter and auto-formatter for Humans. Ported from the
//! none-ls diagnostics/sqlfluff builtin.
//!
//! sqlfluff is run with `-f github-annotation`, which emits a JSON array of
//! GitHub annotation objects: `start_line`/`start_column`/`end_line`/
//! `end_column`/`annotation_level`/`message`. `annotation_level` is one of
//! GitHub's annotation levels (`notice`/`warning`/`failure`).
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "sqlfluff",
	description: "A SQL linter and auto-formatter for Humans",
	url: "https://github.com/sqlfluff/sqlfluff",
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
	json_diag::from_json(stdout, &attrs, severity_of)
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"failure" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		"notice" => Some(severity::INFO),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_github_annotation_array() {
		let json = br#"[
            {"file":"a.sql","start_line":1,"start_column":1,"end_line":1,"end_column":5,
             "annotation_level":"failure","message":"L010: Keywords must be consistently upper case."},
            {"file":"a.sql","start_line":3,"start_column":2,"end_line":3,"end_column":2,
             "annotation_level":"warning","message":"L003: Indentation."}
        ]"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_row, Some(1));
		assert_eq!(out[0].end_col, Some(5));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert!(out[0].message.starts_with("L010"));
		assert_eq!(out[1].severity, Some(severity::WARNING));
	}

	#[test]
	fn maps_notice_to_info() {
		let json = br#"[{"start_line":2,"start_column":1,"annotation_level":"notice","message":"hi"}]"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, Some(severity::INFO));
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}
}
