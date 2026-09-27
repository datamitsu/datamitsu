//! sqruff — a high-speed SQL linter written in Rust.
//!
//! Ported from the none-ls `diagnostics/sqruff` builtin. It runs
//! `sqruff lint --format github-annotation-native {file}` and reads diagnostics
//! from **stderr**. Each line is a GitHub workflow annotation:
//!
//! ```text
//! ::<level> <...>,file=<file>,line=<row>,col=<col>::<rule>: <message>
//! ```
//!
//! e.g. `::error title=sqruff,file=test.sql,line=1,col=1::LT01: Expected only single space.`.
//! The level is a GitHub annotation level, the line and column are 1-based, and
//! no end is printed.
//!
//! none-ls Lua pattern:
//! `^::(%w+) .*,file=(.*),line=(%d+),col=(%d+)::(%w+: .*)`
//! captures (severity, filename, row, col, message).

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "sqruff",
	description: "A high-speed SQL linter written in Rust.",
	url: "https://github.com/quarylabs/sqruff",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("notice", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["lint", "--format", "github-annotation-native", "{file}"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true: diagnostics come from stderr.
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// ::<severity> <...>,file=<file>,line=<row>,col=<col>::<message>
	let rest = line.strip_prefix("::")?;

	// severity = first %w+ token, up to the first space.
	let sp = rest.find(' ')?;
	let severity_tok = &rest[..sp];
	if severity_tok.is_empty() || !severity_tok.chars().all(|c| c.is_alphanumeric() || c == '_') {
		return None;
	}

	// Split the annotation header (before "::") from the message (after).
	let close = rest.find("::")?;
	let header = &rest[..close];

	// The Lua pattern requires the message to be `%w+: .*` (a word + ": ").
	let (code, message) = split_rule(&rest[close + 2..])?;

	let row = field_after(header, ",line=")?;
	let col = field_after(header, ",col=")?;
	// file= must be present (the pattern requires it) but is unused downstream.
	if !header.contains(",file=") {
		return None;
	}

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity::of(DESCRIPTOR.severities, severity_tok),
		code: Some(code.to_string()),
		..RawDiagnostic::default()
	})
}

/// Splits the Lua `(%w+: .*)` message capture into the rule and its text.
fn split_rule(message: &str) -> Option<(&str, &str)> {
	let (word, text) = message.split_once(": ")?;
	let is_rule = !word.is_empty() && word.chars().all(|c| c.is_alphanumeric() || c == '_');
	is_rule.then_some((word, text))
}

/// Parses the unsigned integer immediately following `key` in `header`.
fn field_after(header: &str, key: &str) -> Option<u32> {
	let start = header.find(key)? + key.len();
	let digits: String = header[start..].chars().take_while(|c| c.is_ascii_digit()).collect();
	digits.parse().ok()
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_annotation() {
		let d =
			parse_line("::error title=sqruff,file=test.sql,line=1,col=1::CP01: Keywords must be consistently upper case.")
				.unwrap();
		assert_eq!(d.message, "Keywords must be consistently upper case.");
		assert_eq!(d.code.as_deref(), Some("CP01"));
		assert_eq!(d.row, Some(1));
		assert_eq!(d.col, Some(1));
		assert_eq!(d.end_col, None);
		assert_eq!(d.severity, Some(severity::ERROR));
	}

	#[test]
	fn parse_reads_stderr_and_collects() {
		let out = parse(b"", SAMPLES[0].stderr, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[1].row, Some(10));
		assert_eq!(out[1].col, Some(3));
		assert_eq!(out[1].message, "Keywords must be lower case.");
		assert_eq!(out[1].code.as_deref(), Some("CP01"));
	}

	#[test]
	fn reads_the_other_annotation_levels() {
		let stderr = b"::warning title=sqruff,file=a.sql,line=2,col=1::LT01: w\n\
::notice title=sqruff,file=a.sql,line=3,col=1::LT02: n\n\
::debug title=sqruff,file=a.sql,line=4,col=1::LT03: d\n";
		let levels: Vec<_> = parse(b"", stderr, 1).iter().map(|d| d.severity).collect();
		assert_eq!(levels, [Some(severity::WARNING), Some(severity::INFO), None]);
	}

	#[test]
	fn non_annotation_line_is_skipped() {
		assert!(parse_line("some unrelated output").is_none());
		// message without the `word: ` rule prefix fails the pattern.
		assert!(parse_line("::error file=a.sql,line=1,col=1::just a message").is_none());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"",
	stderr: b"::error title=sqruff,file=a.sql,line=2,col=5::AM04: Query produces an unknown number of result columns.\n\
::error title=sqruff,file=a.sql,line=10,col=3::CP01: Keywords must be lower case.\n",
	exit: 1,
}];
