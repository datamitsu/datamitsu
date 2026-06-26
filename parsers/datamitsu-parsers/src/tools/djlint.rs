//! djlint — HTML Template Linter and Formatter. Ported from the none-ls diagnostics/djlint builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "djlint",
	description: "✨ 📜 🪄 ✨ HTML Template Linter and Formatter.",
	url: "https://github.com/Riverside-Healthcare/djLint",
	operations: &[Operation {
		mode: "lint",
		args: &["--quiet", "-"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stdout).lines().filter_map(parse_line).collect()
}

/// Lua pattern: `(%w+) (%d+):(%d+) (.*).`
/// groups: code, row, col, message ; offsets col +1 ; severity always INFO.
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let line = line.trim_end();
	let (code, rest) = line.split_once(' ')?;
	if code.is_empty() || !code.chars().all(|c| c.is_ascii_alphanumeric() || c == '_') {
		return None;
	}

	let (loc, message) = rest.split_once(' ')?;
	let (row_str, col_str) = loc.split_once(':')?;
	let row: u32 = row_str.parse().ok()?;
	let col: u32 = col_str.parse().ok()?;
	if message.is_empty() {
		return None;
	}

	// The trailing `.` in the pattern is consumed by the final literal dot; the
	// captured message excludes it. Strip one trailing '.' if present.
	let message = message.strip_suffix('.').unwrap_or(message).to_string();

	Some(RawDiagnostic {
		message,
		row: Some(row),
		col: Some(col + 1),
		severity: Some(severity::INFO),
		code: Some(code.to_string()),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_diagnostic_line() {
		let out = b"T001 12:5 Variables should be wrapped in a single whitespace.\n";
		let diags = parse(out, b"", 0);
		assert_eq!(diags.len(), 1);
		let d = &diags[0];
		assert_eq!(d.code.as_deref(), Some("T001"));
		assert_eq!(d.row, Some(12));
		assert_eq!(d.col, Some(6)); // col 5 + offset 1
		assert_eq!(d.severity, Some(severity::INFO));
		assert_eq!(d.message, "Variables should be wrapped in a single whitespace");
	}

	#[test]
	fn parses_multiple_and_skips_noise() {
		let out = b"H006 1:0 Img tag should have height and width attributes.\nnot a diagnostic line at all\nH025 3:2 Tag seems to be orphaned.\n";
		let diags = parse(out, b"", 0);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].code.as_deref(), Some("H006"));
		assert_eq!(diags[0].col, Some(1));
		assert_eq!(diags[1].code.as_deref(), Some("H025"));
		assert_eq!(diags[1].row, Some(3));
	}
}
