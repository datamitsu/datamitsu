//! yamllint — a linter for YAML files.
//!
//! Ported from the none-ls `diagnostics/yamllint` builtin. It runs
//! `yamllint --format parsable -` (stdin) and each output line is:
//!
//! ```text
//! <file>:<row>:<col>: [<level>] <message> (<rule>)
//! ```
//!
//! e.g. `stdin:3:1: [error] too many blank lines (3 > 0) (empty-lines)`. The
//! message itself may contain parentheses, so the **rule is the last** `(...)`
//! group (the none-ls Lua pattern `(.*) %((.*)%)` is greedy on the message).

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "yamllint",
	description: "A linter for YAML files.",
	url: "https://github.com/adrienverge/yamllint",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "parsable", "-"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stdout).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Split at the "[level]" marker: prefix = "<file>:<row>:<col>",
	// rest = "level] <message> (<rule>)".
	let marker = line.find(": [")?;
	let prefix = &line[..marker];
	let rest = &line[marker + 3..];
	let rb = rest.find(']')?;
	let level = &rest[..rb];
	let after = rest[rb + 1..].trim_start();

	let (row, col) = row_col(prefix)?;
	let (message, code) = split_message_rule(after);
	Some(RawDiagnostic {
		message,
		row: Some(row),
		col: Some(col),
		severity: level_severity(level),
		code,
		..RawDiagnostic::default()
	})
}

/// The last two `:`-separated fields of `<file>:<row>:<col>`, read right-to-left
/// so a path containing colons doesn't confuse row/col.
fn row_col(prefix: &str) -> Option<(u32, u32)> {
	let mut it = prefix.rsplit(':');
	let col = it.next()?.trim().parse().ok()?;
	let row = it.next()?.trim().parse().ok()?;
	Some((row, col))
}

/// `message (rule)` → (message, Some(rule)) using the LAST parenthesized group;
/// no trailing `(...)` → (message, None).
fn split_message_rule(after: &str) -> (String, Option<String>) {
	let s = after.trim();
	if s.ends_with(')') {
		if let Some(open) = s.rfind('(') {
			let rule = &s[open + 1..s.len() - 1];
			let msg = s[..open].trim_end();
			return (msg.to_string(), Some(rule.to_string()));
		}
	}
	(s.to_string(), None)
}

fn level_severity(level: &str) -> Option<u8> {
	match level {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_with_rule_and_parens_in_message() {
		let d = parse_line("stdin:3:1: [error] too many blank lines (3 > 0) (empty-lines)").unwrap();
		assert_eq!(d.message, "too many blank lines (3 > 0)");
		assert_eq!(d.row, Some(3));
		assert_eq!(d.col, Some(1));
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.code.as_deref(), Some("empty-lines"));
	}

	#[test]
	fn parses_warning_level() {
		let d = parse_line("stdin:10:5: [warning] line too long (90 > 80 characters) (line-length)").unwrap();
		assert_eq!(d.severity, Some(severity::WARNING));
		assert_eq!(d.code.as_deref(), Some("line-length"));
		assert_eq!((d.row, d.col), (Some(10), Some(5)));
	}

	#[test]
	fn message_without_rule_keeps_full_text() {
		let d = parse_line("stdin:1:1: [error] syntax error: found unexpected end of stream").unwrap();
		assert_eq!(d.message, "syntax error: found unexpected end of stream");
		assert_eq!(d.code, None);
	}

	#[test]
	fn non_matching_line_is_skipped() {
		assert!(parse_line("not a yamllint line").is_none());
	}

	#[test]
	fn parse_collects_multiple_lines() {
		let out = parse(b"stdin:1:1: [warning] a (r1)\nstdin:2:2: [error] b (r2)\n", b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[1].message, "b");
	}
}
