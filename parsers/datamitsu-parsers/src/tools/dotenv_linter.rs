//! dotenv-linter — a lightning-fast linter for `.env` files.
//!
//! Ported from the none-ls `diagnostics/dotenv-linter` builtin (Lua pattern
//! `%w+:(%d+) (%w+): (.*)` → row, code, message). Each output line is:
//!
//! ```text
//! <file>:<row> <CODE>: <message>
//! ```
//!
//! e.g. `.env:2 LowercaseKey: The foo key should be in uppercase`. The tool
//! reports **no column and no severity** — this is the "missing fields" class:
//! the parser leaves `col`/`severity` as `None` and the Go core fills its
//! defaults (col=1, fallback severity).

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "dotenv_linter",
	description: "Lightning-fast linter for .env files.",
	url: "https://github.com/dotenv-linter/dotenv-linter",
	operations: &[Operation {
		mode: "lint",
		args: &["{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stdout).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// "<file>:<row> <CODE>: <message>"
	let sp = line.find(' ')?;
	let left = &line[..sp]; // "<file>:<row>"
	let right = &line[sp + 1..]; // "<CODE>: <message>"

	let row: u32 = left.rsplit(':').next()?.trim().parse().ok()?;
	let colon = right.find(':')?;
	let code = right[..colon].trim();
	let message = right[colon + 1..].trim();
	if code.is_empty() || message.is_empty() {
		return None;
	}
	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		code: Some(code.to_string()),
		// No col, no severity — deliberately left None (the core fills defaults).
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_row_code_message_without_col_or_severity() {
		let d = parse_line(".env:2 LowercaseKey: The foo key should be in uppercase").unwrap();
		assert_eq!(d.row, Some(2));
		assert_eq!(d.code.as_deref(), Some("LowercaseKey"));
		assert_eq!(d.message, "The foo key should be in uppercase");
		assert_eq!(d.col, None, "dotenv-linter reports no column");
		assert_eq!(d.severity, None, "dotenv-linter reports no severity");
	}

	#[test]
	fn skips_lines_without_the_shape() {
		assert!(parse_line("Checking .env").is_none());
		assert!(parse_line("").is_none());
	}

	#[test]
	fn collects_multiple() {
		let out = parse(b".env:1 UnorderedKey: x\n.env:3 DuplicatedKey: y\n", b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[1].code.as_deref(), Some("DuplicatedKey"));
	}
}
