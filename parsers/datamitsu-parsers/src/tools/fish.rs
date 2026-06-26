//! fish — basic linting for fish scripts via `fish --no-execute`.
//!
//! Ported from the none-ls `diagnostics/fish` builtin. It runs
//! `fish --no-execute <file>` and reads diagnostics from **stderr**. The
//! errorformat is a single pattern `%f (line %l): %m`, e.g.:
//!
//! ```text
//! /path/to/script.fish (line 3): Missing end to balance this if statement
//! ```
//!
//! Only file, line, and message are emitted — no column, no severity.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "fish",
	description: "Basic linting is available for fish scripts using `fish --no-execute`.",
	url: "https://github.com/fish-shell/fish-shell",
	operations: &[Operation {
		mode: "lint",
		args: &["--no-execute", "{file}"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

/// Port of the errorformat `%f (line %l): %m`: split on the literal
/// `" (line "` and `"): "` separators to extract line and message.
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let lp = line.find(" (line ")?;
	let after = &line[lp + " (line ".len()..];
	let rp = after.find("): ")?;
	let row: u32 = after[..rp].trim().parse().ok()?;
	let message = after[rp + "): ".len()..].to_string();
	Some(RawDiagnostic {
		message,
		row: Some(row),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_line_and_message() {
		let d = parse_line("/tmp/script.fish (line 3): Missing end to balance this if statement").unwrap();
		assert_eq!(d.message, "Missing end to balance this if statement");
		assert_eq!(d.row, Some(3));
		assert_eq!(d.col, None);
		assert_eq!(d.severity, None);
	}

	#[test]
	fn non_matching_line_is_skipped() {
		assert!(parse_line("fish: this is fine").is_none());
	}

	#[test]
	fn parse_reads_stderr() {
		let out = parse(b"", b"/a.fish (line 1): boom\n/b.fish (line 22): bang\n", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[1].row, Some(22));
		assert_eq!(out[1].message, "bang");
	}
}
