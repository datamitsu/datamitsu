//! markdownlint_cli2 — fast configuration-based CLI linter for Markdown/CommonMark.
//! Ported from the none-ls diagnostics/markdownlint_cli2 builtin.
//!
//! The default formatter prints `<file>:<row>[:<col>] [<level>] <rules> <message>`
//! on stderr. Releases since severities were introduced print the level word
//! (`error` or `warning`); older ones print none, and their findings carry no
//! level.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "markdownlint_cli2",
	description: "A fast, flexible, configuration-based command-line interface for linting Markdown/CommonMark files with the markdownlint library",
	url: "https://github.com/DavidAnson/markdownlint-cli2",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["{file}"],
		stdin: false,
	}],
};

/// Diagnostics are read from stderr (from_stderr = true).
pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Two patterns, each with an optional level word before the code:
//   file:row:col [level] code message
//   file:row [level] code message
// where code is [%w-/]+ (alnum, '-', '/') and filename is %g (non-space).
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let line = line.trim_end();
	// Split off "file:row[:col]" prefix from the rest.
	let space = line.find(' ')?;
	let (locus, rest) = line.split_at(space);
	let rest = rest.trim_start();
	let (severity, rest) = split_level(rest);

	// rest must be "code message"
	let code_end = rest.find(' ')?;
	let code = &rest[..code_end];
	if !is_code(code) {
		return None;
	}
	let message = rest[code_end..].trim_start().to_string();
	if message.is_empty() {
		return None;
	}

	let mut parts = locus.split(':');
	let _file = parts.next()?;
	let row: u32 = parts.next()?.parse().ok()?;
	let col: Option<u32> = match parts.next() {
		Some(c) => Some(c.parse().ok()?),
		None => None,
	};
	if parts.next().is_some() {
		return None;
	}

	Some(RawDiagnostic {
		message,
		row: Some(row),
		col,
		severity,
		code: Some(code.to_string()),
		..RawDiagnostic::default()
	})
}

// A leading level word, when the tool printed one.
fn split_level(rest: &str) -> (Option<u8>, &str) {
	if let Some((word, tail)) = rest.split_once(' ') {
		if let Some(level) = severity::of(DESCRIPTOR.severities, word) {
			return (Some(level), tail.trim_start());
		}
	}
	(None, rest)
}

fn is_code(s: &str) -> bool {
	!s.is_empty() && s.chars().all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '/')
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_row_col() {
		let stderr = b"README.md:3:1 MD041/first-line-heading First line in a file should be a top-level heading";
		let d = parse(b"", stderr, 1);
		assert_eq!(d.len(), 1);
		assert_eq!(d[0].row, Some(3));
		assert_eq!(d[0].col, Some(1));
		assert_eq!(d[0].end_col, None);
		assert_eq!(d[0].code.as_deref(), Some("MD041/first-line-heading"));
		assert_eq!(d[0].message, "First line in a file should be a top-level heading");
	}

	#[test]
	fn parses_row_only() {
		let stderr = b"docs/guide.md:7 MD013/line-length Line length [Expected: 80; Actual: 120]";
		let d = parse(b"", stderr, 1);
		assert_eq!(d.len(), 1);
		assert_eq!(d[0].row, Some(7));
		assert_eq!(d[0].col, None);
		assert_eq!(d[0].code.as_deref(), Some("MD013/line-length"));
		assert_eq!(d[0].message, "Line length [Expected: 80; Actual: 120]");
	}

	#[test]
	fn without_a_level_word_sets_no_severity() {
		let d = parse(b"", SAMPLES[0].stderr, 1);
		assert_eq!(d.len(), 2);
		assert!(d.iter().all(|d| d.severity.is_none()), "{d:?}");
	}

	#[test]
	fn reads_the_printed_level() {
		let d = parse(b"", SAMPLES[1].stderr, 1);
		assert_eq!(d.len(), 2);
		assert_eq!(d[0].severity, Some(severity::ERROR));
		assert_eq!(d[0].row, Some(3));
		assert_eq!(d[0].col, Some(1));
		assert_eq!(d[0].code.as_deref(), Some("MD041/first-line-heading/first-line-h1"));
		assert_eq!(d[0].message, "First line in a file should be a top-level heading");
		assert_eq!(d[1].severity, Some(severity::WARNING));
		assert_eq!(d[1].row, Some(7));
		assert_eq!(d[1].col, None);
		assert_eq!(d[1].code.as_deref(), Some("MD013/line-length"));

		let d = parse(b"", SAMPLES[2].stderr, 0);
		assert_eq!(d.len(), 1);
		assert_eq!(d[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn ignores_summary_lines() {
		let stderr = b"Finding: *.md\nLinting: 1 file(s)\nSummary: 2 error(s)";
		let d = parse(b"", stderr, 1);
		assert!(d.is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr:
			b"README.md:3:1 MD041/first-line-heading/first-line-h1 First line in a file should be a top-level heading\n\
docs/guide.md:7 MD013/line-length Line length [Expected: 80; Actual: 120]\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"Finding: README.md docs/guide.md\nLinting: 2 file(s)\nSummary: 2 error(s)\n",
		stderr:
			b"README.md:3:1 error MD041/first-line-heading/first-line-h1 First line in a file should be a top-level heading\n\
docs/guide.md:7 warning MD013/line-length Line length [Expected: 80; Actual: 120]\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"docs/guide.md:7 warning MD013/line-length Line length [Expected: 80; Actual: 120]\n",
		exit: 0,
	},
];
