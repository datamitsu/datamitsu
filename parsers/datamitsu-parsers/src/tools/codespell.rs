//! codespell — Codespell finds common misspellings in text files.
//! Ported from the none-ls diagnostics/codespell builtin.
//!
//! Output is read `from_stderr`. codespell `-` emits two-line records:
//!   `<row>: - <something>`
//!   `\t<misspelled> ==> <correction>`
//! The Lua pattern `(%d+): - [^\n]+\n\t((%S+)[^\n]+)` captures the row and the
//! full second (message) line. The builtin then derives col/end_col by locating
//! the misspelled token inside the ORIGINAL buffer line (`content[row]`) — buffer
//! content the WASM parser does not receive, so col/end_col are left None and the
//! Go core fills defaults. Row, message, and severity (always WARNING) are ported
//! faithfully.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "codespell",
	description: "Codespell finds common misspellings in text files.",
	url: "https://github.com/codespell-project/codespell",
	operations: &[Operation {
		mode: "lint",
		args: &["-"],
		stdin: true,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stderr);
	let lines: Vec<&str> = text.lines().collect();
	let mut out = Vec::new();
	let mut i = 0;
	while i + 1 < lines.len() {
		// First line: "<digits>: - <rest>"
		if let Some(row) = parse_header_row(lines[i]) {
			let msg_line = lines[i + 1];
			// Message line must start with a tab and contain a non-space token.
			if let Some(message) = parse_message(msg_line) {
				out.push(RawDiagnostic {
					message,
					row: Some(row),
					severity: Some(severity::WARNING),
					source: Some("codespell".to_string()),
					..RawDiagnostic::default()
				});
				i += 2;
				continue;
			}
		}
		i += 1;
	}
	out
}

/// Parse `<digits>: - ...` -> Some(row). Mirrors `(%d+): - [^\n]+`.
fn parse_header_row(line: &str) -> Option<u32> {
	let (num, rest) = split_leading_digits(line)?;
	let rest = rest.strip_prefix(": - ")?;
	if rest.is_empty() {
		return None;
	}
	num.parse::<u32>().ok()
}

/// Parse the `\t(%S+)[^\n]+` message line: a leading tab, then at least one
/// non-space token. Returns the message WITHOUT the leading tab (the Lua capture
/// `((%S+)[^\n]+)` excludes the tab).
fn parse_message(line: &str) -> Option<String> {
	let body = line.strip_prefix('\t')?;
	let mut chars = body.chars();
	let first = chars.next()?;
	if first.is_whitespace() {
		return None; // %S+ requires a non-space start
	}
	Some(body.to_string())
}

fn split_leading_digits(line: &str) -> Option<(&str, &str)> {
	let end = line.find(|c: char| !c.is_ascii_digit())?;
	if end == 0 {
		return None;
	}
	Some((&line[..end], &line[end..]))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_record() {
		let stderr = b"3: - Helllo World\n\thelllo ==> hello\n";
		let d = parse(b"", stderr, 65);
		assert_eq!(d.len(), 1);
		assert_eq!(d[0].row, Some(3));
		assert_eq!(d[0].message, "helllo ==> hello");
		assert_eq!(d[0].severity, Some(severity::WARNING));
		assert_eq!(d[0].source.as_deref(), Some("codespell"));
		assert_eq!(d[0].col, None);
	}

	#[test]
	fn parses_multiple() {
		let stderr = b"1: - teh cat\n\tteh ==> the\n10: - recieve it\n\trecieve ==> receive\n";
		let d = parse(b"", stderr, 65);
		assert_eq!(d.len(), 2);
		assert_eq!(d[0].row, Some(1));
		assert_eq!(d[1].row, Some(10));
		assert_eq!(d[1].message, "recieve ==> receive");
	}

	#[test]
	fn ignores_non_matching() {
		let stderr = b"some preamble\nnot a record\n";
		assert!(parse(b"", stderr, 0).is_empty());
	}
}
