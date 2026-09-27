//! alex — catch insensitive, inconsiderate writing.
//!
//! Ported from the none-ls `diagnostics/alex` builtin. It runs
//! `alex --stdin --quiet` and reports on **stderr** through vfile-reporter. Each
//! diagnostic line is:
//!
//! ```text
//!   <row>:<col>-<end_row>:<end_col>  <severity>  <message>  <ruleId>  <source>
//! ```
//!
//! e.g. `  1:1-1:7   warning  Don't say "master", it may be insensitive  master-slave  retext-equality`.
//!
//! The message is greedy up to the final two whitespace-separated tokens: the
//! `ruleId` (`code`) and the retext plugin that reported it (discarded). Positions
//! are unist points: 1-based, the end naming the column after the span.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "alex",
	description: "Catch insensitive, inconsiderate writing.",
	url: "https://github.com/get-alex/alex",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("info", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--stdin", "--quiet"],
		stdin: true,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let s = line.trim_start();

	// `<row>:<col>-<end_row>:<end_col>` followed by whitespace.
	let dash = s.find('-')?;
	let start = &s[..dash];
	let (row, col) = split_pos(start)?;

	let rest = &s[dash + 1..];
	let ws = rest.find(char::is_whitespace)?;
	let end = &rest[..ws];
	let (end_row, end_col) = split_pos(end)?;

	// After the position span: `<severity>  <message...>  <ruleId>  <source>`.
	let after = rest[ws..].trim_start();
	let sev_end = after.find(char::is_whitespace)?;
	let sev = &after[..sev_end];
	let body = after[sev_end..].trim_start();

	let (before_source, _source) = rsplit_token(body)?;
	let (message, rule) = rsplit_token(before_source.trim_end())?;
	let message = message.trim_end().to_string();
	if message.is_empty() {
		return None;
	}

	Some(RawDiagnostic {
		message,
		row: Some(row),
		col: Some(col),
		end_row: Some(end_row),
		end_col: Some(end_col),
		severity: severity::of(DESCRIPTOR.severities, sev),
		code: Some(rule.to_string()),
		..RawDiagnostic::default()
	})
}

/// `<line>:<column>` → (line, column).
fn split_pos(s: &str) -> Option<(u32, u32)> {
	let (l, c) = s.split_once(':')?;
	Some((l.trim().parse().ok()?, c.trim().parse().ok()?))
}

/// Split off the last whitespace-separated token: returns (head, last_token).
fn rsplit_token(s: &str) -> Option<(&str, &str)> {
	let s = s.trim_end();
	let idx = s.rfind(char::is_whitespace)?;
	Some((&s[..idx], s[idx..].trim_start()))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_warning_line() {
		let line = "  1:1-1:7   warning  Don't say \"master\", it may be insensitive  master-slave  retext-equality";
		let d = parse_line(line).unwrap();
		assert_eq!(d.message, "Don't say \"master\", it may be insensitive");
		assert_eq!((d.row, d.col), (Some(1), Some(1)));
		assert_eq!((d.end_row, d.end_col), (Some(1), Some(7)));
		assert_eq!(d.severity, Some(severity::WARNING));
		assert_eq!(d.code.as_deref(), Some("master-slave"));
		assert_eq!(d.source, None);
	}

	#[test]
	fn parses_error_line() {
		let line = "  3:5-3:14  error  `boogeyman` may be profane  boogeyman  retext-profanities";
		let d = parse_line(line).unwrap();
		assert_eq!(d.message, "`boogeyman` may be profane");
		assert_eq!((d.row, d.col), (Some(3), Some(5)));
		assert_eq!((d.end_row, d.end_col), (Some(3), Some(14)));
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.code.as_deref(), Some("boogeyman"));
	}

	#[test]
	fn an_unknown_level_token_sets_no_severity() {
		let d = parse_line("  2:1-2:4  fatal  msg here  rule-id  retext-equality").unwrap();
		assert_eq!(d.severity, None);
	}

	#[test]
	fn reads_from_stderr_and_skips_noise() {
		let stderr = b"some-file.md\n  1:1-1:7  warning  msg here  rule-id  retext-equality\n  1 warning\n";
		let out = parse(b"", stderr, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "msg here");
		assert_eq!(out[0].code.as_deref(), Some("rule-id"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"",
	stderr: b"<stdin>\n  1:5-1:14  warning  `boogeyman` may be insensitive, use `boogeymonster` instead  boogeyman-boogeywoman  retext-equality\n  1:42-1:48  warning  `master` / `slaves` may be insensitive, use `primary` / `replica` instead  master-slave  retext-equality\n  2:1-2:8  error  Don't use `garbage`, it's profane  garbage  retext-profanities\n\n\xe2\x9a\xa0 3 problems (1 error, 2 warnings)\n",
	exit: 1,
}];
