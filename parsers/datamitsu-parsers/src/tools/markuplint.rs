//! markuplint — A linter for all markup developers. Ported from the none-ls
//! diagnostics/markuplint builtin.
//!
//! markuplint `--format JSON` emits an array of objects with `line`, `col`,
//! `ruleId`, `severity` (a string token) and `message`. The builtin maps both
//! row/end_row from `line` and both col/end_col from `col`, sets a constant
//! `source = "markuplint"`, and runs against a temp file (`$FILENAME`).
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "markuplint",
	description: "A linter for all markup developers.",
	url: "https://github.com/markuplint/markuplint",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "JSON", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let attrs = Attrs {
		row: "line",
		end_row: "line",
		col: "col",
		end_col: "col",
		code: "ruleId",
		message: "message",
		severity: "severity",
	};
	let mut out = json_diag::from_json(stdout, &attrs, severity_of);
	for d in &mut out {
		d.source = Some("markuplint".to_string());
	}
	out
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		"info" => Some(severity::INFO),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_markuplint_json() {
		let json = br#"[
            {"severity":"error","line":3,"col":5,"ruleId":"required-attr","message":"Required 'alt' on '<img>'"},
            {"severity":"warning","line":10,"col":1,"ruleId":"deprecated-element","message":"'<center>' is deprecated"}
        ]"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Required 'alt' on '<img>'");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].end_row, Some(3));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_col, Some(5));
		assert_eq!(out[0].code.as_deref(), Some("required-attr"));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("markuplint"));
		assert_eq!(out[1].severity, Some(severity::WARNING));
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}
}
