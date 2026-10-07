//! djlint — HTML Template Linter and Formatter. Ported from the none-ls diagnostics/djlint builtin.
//!
//! djlint prints no level (a code's letter names the language its rule checks),
//! so severity stays None.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "djlint",
	description: "✨ 📜 🪄 ✨ HTML Template Linter and Formatter.",
	url: "https://github.com/Riverside-Healthcare/djLint",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--quiet", "-"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let lines: Vec<&str> = text.lines().collect();
	let mut out = Vec::new();
	// Linting files (not stdin), djlint heads each file's findings with its path
	// over a rule of `─`.
	let mut file = None;
	for (i, line) in lines.iter().enumerate() {
		if lines.get(i + 1).is_some_and(|next| is_rule(next)) {
			file = crate::diagnostic::file_field(line);
		} else if let Some(mut d) = parse_line(line) {
			d.file.clone_from(&file);
			out.push(d);
		}
	}
	out
}

fn is_rule(line: &str) -> bool {
	let line = line.trim();
	!line.is_empty() && line.chars().all(|c| c == '─')
}

/// Lua pattern: `(%w+) (%d+):(%d+) (.*).`
/// groups: code, row, col, message ; djlint counts columns from 0, so col +1.
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
		col: col.checked_add(1),
		code: Some(code.to_string()),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const OUTPUT: &[u8] = b"H006 1:0 Img tag should have height and width attributes.\nnot a diagnostic line at all\nH025 3:2 Tag seems to be orphaned.\n";

	#[test]
	fn parses_diagnostic_line() {
		let out = b"T001 12:5 Variables should be wrapped in a single whitespace.\n";
		let diags = parse(out, b"", 0);
		assert_eq!(diags.len(), 1);
		let d = &diags[0];
		assert_eq!(d.code.as_deref(), Some("T001"));
		assert_eq!(d.row, Some(12));
		assert_eq!(d.col, Some(6)); // col 5 + offset 1
		assert_eq!(d.message, "Variables should be wrapped in a single whitespace");
	}

	#[test]
	fn parses_multiple_and_skips_noise() {
		let diags = parse(OUTPUT, b"", 0);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].code.as_deref(), Some("H006"));
		assert_eq!(diags[0].col, Some(1));
		assert_eq!(diags[1].code.as_deref(), Some("H025"));
		assert_eq!(diags[1].row, Some(3));
	}

	#[test]
	fn never_sets_a_severity() {
		let diags = parse(OUTPUT, b"", 1);
		assert_eq!(diags.len(), 2);
		assert!(diags.iter().all(|d| d.severity.is_none()));
	}

	#[test]
	fn each_finding_names_the_file_of_its_header() {
		let out = "\ntemplates/a.html\n──────────\nH006 1:0 first.\nH025 2:0 second.\n\n\
templates/b.html\n──────────\nT001 3:1 third.\n\nLinted 2 files, found 3 errors.\n";
		let got: Vec<_> = parse(out.as_bytes(), b"", 1)
			.into_iter()
			.map(|d| (d.message, d.file))
			.collect();
		assert_eq!(
			got,
			[
				("first".to_string(), Some("templates/a.html".to_string())),
				("second".to_string(), Some("templates/a.html".to_string())),
				("third".to_string(), Some("templates/b.html".to_string())),
			]
		);
		// Linting stdin prints no header.
		assert!(parse(OUTPUT, b"", 1).iter().all(|d| d.file.is_none()));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: tests::OUTPUT,
	stderr: b"",
	exit: 1,
}];
