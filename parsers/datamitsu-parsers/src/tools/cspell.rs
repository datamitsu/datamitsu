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
	// Location "<file>:<row>:<col>"; the path is what one cspell run over a whole
	// project needs to report. `find(" - ")` takes the FIRST separator, so a file
	// literally named "12:30 - notes.md" leaves a location with no path at all —
	// hence the splitter, which yields "" instead of wrapping below zero.
	let (file, row, col) = crate::location::file_row_col(&line[..dash])?;
	Some(RawDiagnostic {
		message,
		row: Some(row),
		col: Some(col),
		file: crate::diagnostic::file_field(file),
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
	#[test]
	fn reports_the_path() {
		let d = parse_line("docs/guide.md:3:6 - Unknown word (sentense)").unwrap();
		assert_eq!(d.file.as_deref(), Some("docs/guide.md"));
		assert_eq!((d.row, d.col), (Some(3), Some(6)));
	}
	#[test]
	fn a_location_without_a_path_does_not_panic() {
		// `find(" - ")` splits at the FIRST separator, so a filename containing
		// " - " leaves a bare "row:col". The old length arithmetic underflowed
		// here, which in a panic=abort WASM build trapped the module and cost the
		// whole run's diagnostics.
		let d = parse_line("12:30 - notes.md:1:5 - Unknown word (teh)").unwrap();
		assert_eq!((d.row, d.col), (Some(12), Some(30)));
		assert_eq!(d.file, None, "no path in the location -> the core stamps it");
	}
}
