//! markdownlint — Markdown style and syntax checker. Ported from the none-ls
//! diagnostics/markdownlint builtin.
//!
//! markdownlint-cli prints `<file>:<row>[:<col>] [<level>] <rules> <message>` on
//! stderr. Releases since severities were introduced print the level word
//! (`error` or `warning`); older ones print none, and their findings carry no
//! level.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "markdownlint",
	description: "Markdown style and syntax checker.",
	url: "https://github.com/DavidAnson/markdownlint",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
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

// Two Lua patterns, each anchored to a leading ':' (the filename precedes it):
//   :(%d+):(%d+) ([%w-/]+) (.*)   -> row, col, code, message
//   :(%d+) ([%w-/]+) (.*)         -> row, code, message
// with an optional level word before the code. We locate the first ":<digits>"
// boundary and parse the remainder.
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

	let (col, tail) = match after_row.strip_prefix(':') {
		Some(after_colon) => {
			let col_end = after_colon.find(|c: char| !c.is_ascii_digit())?;
			let col: u32 = after_colon[..col_end].parse().ok()?;
			(Some(col), after_colon[col_end..].strip_prefix(' ')?)
		}
		None => (None, after_row.strip_prefix(' ')?),
	};
	let (severity, tail) = split_level(tail);
	let (code, message) = split_code_message(tail)?;
	Some(RawDiagnostic {
		message,
		row: Some(row),
		col,
		code: Some(code),
		severity,
		..RawDiagnostic::default()
	})
}

// A leading level word, when the tool printed one.
fn split_level(tail: &str) -> (Option<u8>, &str) {
	if let Some((word, rest)) = tail.split_once(' ') {
		if let Some(level) = severity::of(DESCRIPTOR.severities, word) {
			return (Some(level), rest);
		}
	}
	(None, tail)
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
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[0].code.as_deref(), Some("MD009/no-trailing-spaces"));
		assert_eq!(out[0].message, "Trailing spaces [Expected: 0]");
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
	}

	#[test]
	fn without_a_level_word_sets_no_severity() {
		let out = parse(&[], SAMPLES[0].stderr, 1);
		assert_eq!(out.len(), 2);
		assert!(out.iter().all(|d| d.severity.is_none()), "{out:?}");
	}

	#[test]
	fn reads_the_printed_level() {
		let out = parse(&[], SAMPLES[1].stderr, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].row, Some(18));
		assert_eq!(out[0].col, Some(3));
		assert_eq!(out[0].code.as_deref(), Some("MD009/no-trailing-spaces"));
		assert_eq!(out[0].message, "Trailing spaces [Expected: 0; Actual: 1]");
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].row, Some(1));
		assert_eq!(out[1].col, None);
		assert_eq!(out[1].code.as_deref(), Some("MD041/first-line-heading/first-line-h1"));

		let out = parse(&[], SAMPLES[2].stderr, 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn ignores_unrelated_lines() {
		let out = parse(&[], b"A configuration error message without coordinates", 1);
		assert!(out.is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: b"stdin:18:3 MD009/no-trailing-spaces Trailing spaces [Expected: 0; Actual: 1]\n\
stdin:1 MD041/first-line-heading/first-line-h1 First line in a file should be a top-level heading\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"stdin:18:3 error MD009/no-trailing-spaces Trailing spaces [Expected: 0; Actual: 1]\n\
stdin:1 warning MD041/first-line-heading/first-line-h1 First line in a file should be a top-level heading\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"stdin:4:81 warning MD013/line-length Line length [Expected: 80; Actual: 96]\n",
		exit: 0,
	},
];
