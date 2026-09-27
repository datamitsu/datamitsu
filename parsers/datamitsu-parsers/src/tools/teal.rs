//! teal — the compiler for Teal, a typed dialect of Lua. Ported from the
//! none-ls diagnostics/teal builtin.
//!
//! `tl check` emits, on stderr, section headers like `5 errors:`, `2 warnings:`
//! or `1 syntax error:`, then lines of the form `<file>:<row>:<col>: <message>`.
//! The header's category is the level token for the lines that follow it; a line
//! before any header, or under a header the vocabulary does not list, has no
//! level. The upstream builtin also filtered by temp_path and derived `end_col`
//! from the buffer quote — both require the vim runtime/buffer content, which is
//! unavailable here, so they are intentionally dropped.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "teal",
	description: "The compiler for Teal, a typed dialect of Lua.",
	url: "https://github.com/teal-language/tl",
	severities: &[
		Level("error", severity::ERROR),
		Level("errors", severity::ERROR),
		Level("syntax error", severity::ERROR),
		Level("syntax errors", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("warnings", severity::WARNING),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["check", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true; fall back to stdout if stderr is empty.
	let text = if stderr.is_empty() {
		String::from_utf8_lossy(stdout)
	} else {
		String::from_utf8_lossy(stderr)
	};

	let mut out = Vec::new();
	let mut current = None;
	for raw in text.split('\n') {
		let line = raw.strip_suffix('\r').unwrap_or(raw);
		if let Some(category) = header_category(line) {
			current = severity::of(DESCRIPTOR.severities, category);
			continue;
		}
		if let Some(diag) = parse_diag(line, current) {
			out.push(diag);
		}
	}
	out
}

/// `<count> <category>:` — a section header such as `2 warnings:` or
/// `1 syntax error:`. Returns the category.
fn header_category(line: &str) -> Option<&str> {
	let body = line.strip_suffix(':')?;
	let (count, category) = body.split_once(' ')?;
	if count.is_empty() || !count.chars().all(|c| c.is_ascii_digit()) {
		return None;
	}
	if category.is_empty() || !category.chars().all(|c| c.is_ascii_alphanumeric() || c == ' ') {
		return None;
	}
	Some(category)
}

/// `([^:]+):(%d+):(%d+): (.*)$` — file:row:col: message.
fn parse_diag(line: &str, severity: Option<u8>) -> Option<RawDiagnostic> {
	// file = up to the first ':'
	let (file, rest) = line.split_once(':')?;
	if file.is_empty() {
		return None;
	}
	let (row_s, rest) = rest.split_once(':')?;
	let (col_s, rest) = rest.split_once(':')?;
	// message follows "col: " — require the single space after the colon.
	let message = rest.strip_prefix(' ')?;

	let row: u32 = row_s.parse().ok()?;
	let col: u32 = col_s.parse().ok()?;
	if message.is_empty() {
		return None;
	}

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity,
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_errors_section() {
		let stderr = b"2 errors:\nfoo.tl:3:10: unknown variable: x\nfoo.tl:7:1: redeclaration of 'y'\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].message, "unknown variable: x");
		assert_eq!(diags[0].row, Some(3));
		assert_eq!(diags[0].col, Some(10));
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[1].message, "redeclaration of 'y'");
		assert_eq!(diags[1].col, Some(1));
	}

	#[test]
	fn warning_header_switches_severity() {
		let stderr = b"1 warning:\nbar.tl:5:2: unused variable z\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].severity, Some(severity::WARNING));
		assert_eq!(diags[0].row, Some(5));
	}

	#[test]
	fn a_syntax_error_header_is_a_level() {
		let stderr = b"1 warning:\na.tl:1:7: unused variable x\n1 syntax error:\nb.tl:2:1: syntax error\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].severity, Some(severity::WARNING));
		assert_eq!(diags[1].severity, Some(severity::ERROR));
	}

	#[test]
	fn no_level_without_a_header() {
		let stderr = b"baz.tl:1:1: syntax error\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].severity, None);
		assert_eq!(diags[0].message, "syntax error");
	}

	#[test]
	fn an_unknown_header_clears_the_level() {
		let stderr = b"1 error:\na.tl:1:1: x\n2 notes:\na.tl:2:1: y\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[1].severity, None);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: b"========================================\n1 warning:\nfoo.tl:1:7: unused variable x: integer\n========================================\n2 errors:\nfoo.tl:3:10: unknown variable: y\nfoo.tl:7:1: redeclaration of variable 'z'\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"========================================\n1 syntax error:\nbar.tl:2:1: syntax error, expected 'end'\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"========================================\n2 warnings:\nbaz.tl:4:9: unused variable a: string\nbaz.tl:5:9: unused variable b: string\n",
		exit: 0,
	},
];
