//! editorconfig_checker — verify files match their `.editorconfig`.
//! Ported from the none-ls diagnostics/editorconfig_checker builtin.
//!
//! Upstream emits a file header line followed by indented `<row>: <message>`
//! lines on **stdout** (the none-ls `from_stderr = true` is wrong for current
//! versions), so read stdout first and fall back to stderr. The none-ls pattern
//! `(%d+): (.+)` captures only `row` and `message`; no column/severity/code.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "editorconfig_checker",
	description: "A tool to verify that your files are in harmony with your `.editorconfig`.",
	url: "https://github.com/editorconfig-checker/editorconfig-checker",
	// none-ls: args ["-no-color", "$FILENAME"], to_stdin=true, from_stderr=true.
	operations: &[Operation {
		mode: "lint",
		args: &["-no-color", "{file}"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// stdout-first: editorconfig-checker writes diagnostics to stdout, with a
	// stderr fallback for robustness.
	let bytes = if stdout.is_empty() { stderr } else { stdout };
	String::from_utf8_lossy(bytes).lines().filter_map(parse_line).collect()
}

/// Port of the Lua pattern `(%d+): (.+)`: leading run of digits, then `": "`,
/// then the (non-empty) rest of the line as the message. Lines that do not
/// match (e.g. the file-name header) are skipped.
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let trimmed = line.trim_start();
	let digits_end = trimmed.find(|c: char| !c.is_ascii_digit())?;
	if digits_end == 0 {
		return None; // no leading digits
	}
	let row: u32 = trimmed[..digits_end].parse().ok()?;
	let rest = &trimmed[digits_end..];
	let message = rest.strip_prefix(": ")?;
	if message.is_empty() {
		return None; // pattern requires `.+`
	}
	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	const SAMPLE: &[u8] = b"src/main.rs:\n\t3: Wrong line endings or no final newline\n\t10: Trailing whitespace\n";

	#[test]
	fn parses_row_and_message_from_stdout() {
		// The report arrives on stdout, the stream this tool actually writes to.
		let diags = parse(SAMPLE, b"", 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].row, Some(3));
		assert_eq!(diags[0].message, "Wrong line endings or no final newline");
		assert_eq!(diags[0].col, None);
		assert_eq!(diags[0].severity, None);
		assert_eq!(diags[1].row, Some(10));
		assert_eq!(diags[1].message, "Trailing whitespace");
	}

	#[test]
	fn falls_back_to_stderr_when_stdout_empty() {
		assert_eq!(parse(b"", SAMPLE, 1).len(), 2);
	}

	#[test]
	fn skips_non_matching_lines() {
		// The file header has no `<digits>: ` prefix and must be ignored.
		let diags = parse(b"path/to/file.txt:\n", b"", 1);
		assert!(diags.is_empty());
	}
}
