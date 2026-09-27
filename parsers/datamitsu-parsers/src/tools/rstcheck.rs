//! rstcheck — Checks syntax of reStructuredText and code blocks nested within it.
//! Ported from the none-ls diagnostics/rstcheck builtin.
//!
//! rstcheck writes findings to stderr in the form
//! `<file>:<row>: (<LEVEL>/<num>) <message>`, e.g.
//! `doc.rst:12: (ERROR/3) Unknown directive type "foo".`
//! The Lua pattern `([^:]+):(%d+): %((.+)/%d%) (.+)` captures
//! filename, row, docutils level word, and message. The row is 1-based; no
//! column is printed.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "rstcheck",
	description: "Checks syntax of reStructuredText and code blocks nested within it.",
	url: "https://github.com/myint/rstcheck",
	severities: &[
		Level("SEVERE", severity::ERROR),
		Level("ERROR", severity::ERROR),
		Level("WARNING", severity::WARNING),
		Level("INFO", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-r", "{file}"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// ([^:]+):(%d+): %((.+)/%d%) (.+)
	// First ":" splits filename from the rest.
	let (_file, rest) = line.split_once(':')?;
	// Next field is the row number, terminated by ": ".
	let (row_str, rest) = rest.split_once(": ")?;
	let row: u32 = row_str.trim().parse().ok()?;

	// rest begins with "(LEVEL/N) message"
	let rest = rest.strip_prefix('(')?;
	let (paren, message) = rest.split_once(") ")?;
	// paren = "LEVEL/N" — the level is everything before the last "/".
	let level = paren.rsplit_once('/').map(|(l, _)| l).unwrap_or(paren);

	if message.is_empty() {
		return None;
	}

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		severity: severity::of(DESCRIPTOR.severities, level),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_line() {
		let out = parse(b"", SAMPLES[0].stderr, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Unknown directive type \"foo\".");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].col, None);
	}

	#[test]
	fn parses_levels() {
		let out = parse(b"", SAMPLES[1].stderr, 1);
		assert_eq!(out.len(), 3);
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[1].severity, Some(severity::INFO));
		assert_eq!(out[1].row, Some(5));
		assert_eq!(out[2].severity, Some(severity::ERROR));
	}

	#[test]
	fn an_unknown_level_word_has_no_level() {
		let out = parse(b"", b"a.rst:3: (DEBUG/0) Something internal.\n", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn ignores_non_matching_lines() {
		let stderr = b"some unrelated output\nWARNING: rstcheck banner\n";
		let out = parse(b"", stderr, 0);
		assert!(out.is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: b"doc.rst:12: (ERROR/3) Unknown directive type \"foo\".\nError! Issues detected.\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"a.rst:1: (WARNING/2) Title underline too short.\n\
a.rst:5: (INFO/1) Hyperlink target is not referenced.\n\
a.rst:9: (SEVERE/4) Unexpected section title.\n\
Error! Issues detected.\n",
		exit: 1,
	},
];
