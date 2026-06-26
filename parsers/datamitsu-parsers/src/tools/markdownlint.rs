//! markdownlint — Markdown style and syntax checker. Ported from the none-ls
//! diagnostics/markdownlint builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "markdownlint",
	description: "Markdown style and syntax checker.",
	url: "https://github.com/DavidAnson/markdownlint",
	// to_stdin = true, args = { "--stdin" }
	operations: &[Operation {
		mode: "lint",
		args: &["--stdin"],
		stdin: true,
	}],
};

// from_stderr = true: diagnostics arrive on stderr.
pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Two Lua patterns, both severity = 2 (WARNING):
//   :(%d+):(%d+) ([%w-/]+) (.*)   -> row, col, code, message
//   :(%d+) ([%w-/]+) (.*)         -> row, code, message
// Each is anchored to a leading ':' (the filename precedes it). We locate the
// first ":<digits>" boundary and parse the remainder.
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Find a ':' immediately followed by a digit (the ":<row>" boundary).
	let bytes = line.as_bytes();
	let mut idx = None;
	for (i, &b) in bytes.iter().enumerate() {
		if b == b':' && bytes.get(i + 1).is_some_and(u8::is_ascii_digit) {
			idx = Some(i);
			break;
		}
	}
	let rest = &line[idx? + 1..]; // after the ':'

	// row: leading digits
	let row_end = rest.find(|c: char| !c.is_ascii_digit())?;
	let row: u32 = rest[..row_end].parse().ok()?;
	let after_row = &rest[row_end..];

	if let Some(after_colon) = after_row.strip_prefix(':') {
		// Pattern 1: :row:col code message
		let col_end = after_colon.find(|c: char| !c.is_ascii_digit())?;
		let col: u32 = after_colon[..col_end].parse().ok()?;
		let tail = after_colon[col_end..].strip_prefix(' ')?;
		let (code, message) = split_code_message(tail)?;
		return Some(RawDiagnostic {
			message,
			row: Some(row),
			col: Some(col),
			code: Some(code),
			severity: Some(severity::WARNING),
			..RawDiagnostic::default()
		});
	}

	// Pattern 2: :row code message
	let tail = after_row.strip_prefix(' ')?;
	let (code, message) = split_code_message(tail)?;
	Some(RawDiagnostic {
		message,
		row: Some(row),
		code: Some(code),
		severity: Some(severity::WARNING),
		..RawDiagnostic::default()
	})
}

// "([%w-/]+) (.*)": code is one run of word chars, '-' or '/'; message is the rest.
fn split_code_message(tail: &str) -> Option<(String, String)> {
	let code_end = tail.find(|c: char| !(c.is_ascii_alphanumeric() || c == '_' || c == '-' || c == '/'))?;
	if code_end == 0 {
		return None;
	}
	let code = tail[..code_end].to_string();
	let message = tail[code_end..].strip_prefix(' ')?.to_string();
	Some((code, message))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_row_col_code_message() {
		let stderr = b"stdin:18:3 MD009/no-trailing-spaces Trailing spaces [Expected: 0]";
		let out = parse(&[], stderr, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(18));
		assert_eq!(out[0].col, Some(3));
		assert_eq!(out[0].code.as_deref(), Some("MD009/no-trailing-spaces"));
		assert_eq!(out[0].message, "Trailing spaces [Expected: 0]");
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn parses_row_only() {
		let stderr = b"stdin:1 MD041/first-line-heading First line in a file should be a top-level heading";
		let out = parse(&[], stderr, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].code.as_deref(), Some("MD041/first-line-heading"));
		assert_eq!(out[0].message, "First line in a file should be a top-level heading");
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn ignores_unrelated_lines() {
		let out = parse(&[], b"A configuration error message without coordinates", 1);
		assert!(out.is_empty());
	}
}
