//! statix — Lints and suggestions for the Nix programming language.
//! Ported from the none-ls diagnostics/statix builtin.
//!
//! statix runs `check --stdin --format=errfmt` and writes to stderr. Each
//! diagnostic line matches the Lua pattern `>(%d+):(%d+):(.):(%d+):(.*)`:
//! `>row:col:severityChar:code:message`. The severity char is `E` (error) or
//! `W` (warning); anything else maps to None. The numeric `code` is carried as
//! the diagnostic code string.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "statix",
	description: "Lints and suggestions for the Nix programming language.",
	url: "https://github.com/nerdypepper/statix",
	operations: &[Operation {
		mode: "lint",
		args: &["check", "--stdin", "--format=errfmt"],
		stdin: true,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true: statix writes its errfmt output to stderr.
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Pattern: `>row:col:severityChar:code:message`
	let rest = line.strip_prefix('>')?;

	// Split into the first four colon-delimited fields plus the trailing message,
	// which may itself contain colons (Lua `.*` is greedy but here the message is
	// the final capture).
	let mut parts = rest.splitn(5, ':');
	let row: u32 = parts.next()?.parse().ok()?;
	let col: u32 = parts.next()?.parse().ok()?;
	let sev_char = parts.next()?;
	let code = parts.next()?;
	let message = parts.next()?;

	if message.is_empty() && code.is_empty() {
		return None;
	}

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity_of(sev_char),
		code: Some(code.to_string()),
		..RawDiagnostic::default()
	})
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"E" => Some(severity::ERROR),
		"W" => Some(severity::WARNING),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_warning_and_error() {
		let stderr = b">3:1:W:4:Assignment instead of inherit from\n\
                       >7:5:E:1:found problem with this expression";
		let out = parse(b"", stderr, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].code.as_deref(), Some("4"));
		assert_eq!(out[0].message, "Assignment instead of inherit from");
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[1].code.as_deref(), Some("1"));
	}

	#[test]
	fn message_may_contain_colons() {
		let out = parse(b"", b">10:2:W:6:note: see https://example.com", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "note: see https://example.com");
	}

	#[test]
	fn ignores_non_diagnostic_lines() {
		let out = parse(b"", b"some other output\n", 0);
		assert!(out.is_empty());
	}
}
