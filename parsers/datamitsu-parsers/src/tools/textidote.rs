//! textidote — spelling, grammar and style checking on LaTeX documents.
//!
//! Ported from the none-ls `diagnostics/textidote` builtin. It runs textidote
//! with `--output singleline`, producing lines of the form:
//!
//! ```text
//! <message>(L<row>C<col>-L<end_row>C<end_col>): <message>
//! ```
//!
//! The none-ls Lua pattern is
//! `(.*)%(L(%d+)C(%d+)-L(%d+)C(%d+)%): (.*)` with groups
//! `filename, row, col, end_row, end_col, message`. The first `(.*)` (greedy
//! prefix, captured as "filename") is discarded — the actual diagnostic text is
//! the trailing `(.*)` after the colon. Severity is fixed to warning.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "textidote",
	description: "Spelling, grammar and style checking on LaTeX documents.",
	url: "https://github.com/sylvainhalle/textidote",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"--read-all",
			"--output",
			"singleline",
			"--no-color",
			"--check",
			"en",
			"--quiet",
			"{file}",
		],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stdout).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Locate the position marker `(L<row>C<col>-L<end_row>C<end_col>): `.
	// The Lua prefix `(.*)` is greedy, so anchor on the LAST `(L` that is
	// followed by a well-formed marker terminated by `): `.
	let mut search_from = 0usize;
	loop {
		let rel = line[search_from..].find("(L")?;
		let open = search_from + rel;
		if let Some(d) = try_marker(line, open) {
			return Some(d);
		}
		search_from = open + 2;
	}
}

fn try_marker(line: &str, open: usize) -> Option<RawDiagnostic> {
	let close = line[open..].find("): ")?;
	let inner = &line[open + 1..open + close]; // "L<row>C<col>-L<end_row>C<end_col>"
	let message = line[open + close + 3..].to_string();

	let (start, end) = inner.split_once('-')?;
	let (row, col) = parse_lc(start)?;
	let (end_row, end_col) = parse_lc(end)?;

	Some(RawDiagnostic {
		message,
		row: Some(row),
		col: Some(col),
		end_row: Some(end_row),
		end_col: Some(end_col),
		severity: Some(severity::WARNING),
		..RawDiagnostic::default()
	})
}

/// Parse a `L<line>C<col>` token into `(line, col)`.
fn parse_lc(tok: &str) -> Option<(u32, u32)> {
	let rest = tok.strip_prefix('L')?;
	let (l, c) = rest.split_once('C')?;
	Some((l.parse().ok()?, c.parse().ok()?))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_position_and_message() {
		let d = parse_line(r#"Possible spelling mistake found. (L12C5-L12C10): "teh" -> "the""#).unwrap();
		assert_eq!(d.message, r#""teh" -> "the""#);
		assert_eq!((d.row, d.col), (Some(12), Some(5)));
		assert_eq!((d.end_row, d.end_col), (Some(12), Some(10)));
		assert_eq!(d.severity, Some(severity::WARNING));
	}

	#[test]
	fn non_matching_line_is_skipped() {
		assert!(parse_line("textidote: nothing to report").is_none());
	}

	#[test]
	fn parse_collects_multiple_lines() {
		let out = parse(b"foo (L1C1-L1C3): a\nbar (L2C4-L3C2): b\n", b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[1].message, "b");
		assert_eq!((out[1].row, out[1].col), (Some(2), Some(4)));
		assert_eq!((out[1].end_row, out[1].end_col), (Some(3), Some(2)));
	}
}
