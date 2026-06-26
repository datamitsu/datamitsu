//! cspell — a spell checker for code. Default output line:
//!
//! ```text
//! <file>:<row>:<col> - <message>
//! ```
//!
//! e.g. `doc.md:3:6 - Unknown word (sentense) fix: (sentence)`. cspell reports no
//! severity (the core supplies its fallback); the offending word is inside the
//! message. Progress/summary lines lack the ` - ` separator and are skipped.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "cspell",
	description: "A spell checker for code.",
	url: "https://cspell.org",
	operations: &[Operation {
		mode: "lint",
		args: &["lint", "{files}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let bytes = if stdout.is_empty() { stderr } else { stdout };
	String::from_utf8_lossy(bytes).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let dash = line.find(" - ")?;
	let message = line[dash + 3..].trim().to_string();
	if message.is_empty() {
		return None;
	}
	// Location "<file>:<row>:<col>", read row/col right-to-left.
	let mut it = line[..dash].rsplit(':');
	let col: u32 = it.next()?.trim().parse().ok()?;
	let row: u32 = it.next()?.trim().parse().ok()?;
	Some(RawDiagnostic {
		message,
		row: Some(row),
		col: Some(col),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_unknown_word_line() {
		let d = parse_line("doc.md:3:6 - Unknown word (sentense) fix: (sentence)").unwrap();
		assert_eq!((d.row, d.col), (Some(3), Some(6)));
		assert_eq!(d.message, "Unknown word (sentense) fix: (sentence)");
		assert_eq!(d.severity, None, "cspell reports no severity");
	}

	#[test]
	fn skips_progress_lines() {
		assert!(parse_line("1/1 doc.md 854.42ms X").is_none());
		assert!(parse_line("").is_none());
	}

	#[test]
	fn collects_multiple() {
		let out = parse(
			b"1/1 doc.md 12ms X\ndoc.md:3:6 - Unknown word (sentense)\ndoc.md:3:21 - Unknown word (mispeled)\n",
			b"",
			1,
		);
		assert_eq!(out.len(), 2);
		assert_eq!(out[1].col, Some(21));
	}
}
