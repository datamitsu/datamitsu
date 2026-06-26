//! sqruff — a high-speed SQL linter written in Rust.
//!
//! Ported from the none-ls `diagnostics/sqruff` builtin. It runs
//! `sqruff lint --format github-annotation-native {file}` and reads diagnostics
//! from **stderr**. Each line is a GitHub annotation:
//!
//! ```text
//! ::<severity> <...>,file=<file>,line=<row>,col=<col>::<rule>: <message>
//! ```
//!
//! e.g. `::error title=sqruff,file=test.sql,line=1,col=1::L010: Keywords must be consistently upper case.`.
//!
//! none-ls Lua pattern:
//! `^::(%w+) .*,file=(.*),line=(%d+),col=(%d+)::(%w+: .*)`
//! captures (severity, filename, row, col, message).

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "sqruff",
	description: "A high-speed SQL linter written in Rust.",
	url: "https://github.com/quarylabs/sqruff",
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
	let message = &rest[close + 2..];

	// The Lua pattern requires the message to be `%w+: .*` (a word + ": ").
	if !message_is_rule_prefixed(message) {
		return None;
	}

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
		severity: severity_of(severity_tok),
		..RawDiagnostic::default()
	})
}

/// Matches the Lua `(%w+: .*)` message capture: a word followed by ": ".
fn message_is_rule_prefixed(message: &str) -> bool {
	let Some(colon) = message.find(": ") else {
		return false;
	};
	let word = &message[..colon];
	!word.is_empty() && word.chars().all(|c| c.is_alphanumeric() || c == '_')
}

/// Parses the unsigned integer immediately following `key` in `header`.
fn field_after(header: &str, key: &str) -> Option<u32> {
	let start = header.find(key)? + key.len();
	let digits: String = header[start..].chars().take_while(|c| c.is_ascii_digit()).collect();
	digits.parse().ok()
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"error" => Some(severity::ERROR),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_annotation() {
		let d =
			parse_line("::error title=sqruff,file=test.sql,line=1,col=1::L010: Keywords must be consistently upper case.")
				.unwrap();
		assert_eq!(d.message, "L010: Keywords must be consistently upper case.");
		assert_eq!(d.row, Some(1));
		assert_eq!(d.col, Some(1));
		assert_eq!(d.severity, Some(severity::ERROR));
	}

	#[test]
	fn parse_reads_stderr_and_collects() {
		let stderr = b"::error title=sqruff,file=a.sql,line=2,col=5::L044: Query produces an unknown number of result columns.\n::error title=sqruff,file=a.sql,line=10,col=3::CP01: Keywords must be lower case.\n";
		let out = parse(b"", stderr, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[1].row, Some(10));
		assert_eq!(out[1].col, Some(3));
		assert_eq!(out[1].message, "CP01: Keywords must be lower case.");
	}

	#[test]
	fn non_annotation_line_is_skipped() {
		assert!(parse_line("some unrelated output").is_none());
		// message without the `word: ` rule prefix fails the pattern.
		assert!(parse_line("::error file=a.sql,line=1,col=1::just a message").is_none());
	}
}
