//! statix — Lints and suggestions for the Nix programming language.
//! Ported from the none-ls diagnostics/statix builtin.
//!
//! statix runs `check --stdin --format=errfmt` and writes to stderr. Each
//! diagnostic line is vim's `%f>%l:%c:%t:%n:%m` — `<file>>row:col:level:code:message`
//! with a 1-based row and column and no end — matched, like the builtin's
//! unanchored Lua pattern `>(%d+):(%d+):(.):(%d+):(.*)`, after the file, which
//! names the finding's file. The level is `E`, `W` or `I` (statix's hint); the
//! numeric code is the lint.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "statix",
	description: "Lints and suggestions for the Nix programming language.",
	url: "https://github.com/nerdypepper/statix",
	severities: &[
		Level("E", severity::ERROR),
		Level("W", severity::WARNING),
		Level("I", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
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
	line.match_indices('>').find_map(|(i, _)| {
		let mut d = parse_fields(&line[i + 1..])?;
		// `<stdin>` for what --stdin read.
		d.file = crate::diagnostic::file_field(&line[..i]);
		Some(d)
	})
}

/// `row:col:level:code:message`, the part after the file's `>`.
fn parse_fields(rest: &str) -> Option<RawDiagnostic> {
	// The message is the last field and may itself contain colons.
	let mut parts = rest.splitn(5, ':');
	let row: u32 = parts.next()?.parse().ok()?;
	let col: u32 = parts.next()?.parse().ok()?;
	let level = parts.next()?;
	let code = parts.next()?;
	let message = parts.next()?;

	if level.chars().count() != 1 || code.is_empty() || !code.bytes().all(|b| b.is_ascii_digit()) {
		return None;
	}

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity::of(DESCRIPTOR.severities, level),
		code: Some(code.to_string()),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_warning_and_error() {
		let out = parse(b"", SAMPLES[0].stderr, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].code.as_deref(), Some("4"));
		assert_eq!(out[0].message, "Assignment instead of inherit from");
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[1].code.as_deref(), Some("0"));
	}

	#[test]
	fn reads_the_hint_level() {
		let out = parse(b"", SAMPLES[1].stderr, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, Some(severity::INFO));
		assert_eq!((out[0].row, out[0].col), (Some(2), Some(7)));
	}

	#[test]
	fn a_line_without_a_file_still_parses() {
		let out = parse(b"", b">3:1:W:4:Assignment instead of inherit from\n", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(3));
	}

	#[test]
	fn an_unknown_level_has_none() {
		let out = parse(b"", b"<stdin>>1:1:X:4:m\n", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn message_may_contain_colons() {
		let out = parse(b"", b"<stdin>>10:2:W:6:note: see https://example.com", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "note: see https://example.com");
	}

	#[test]
	fn ignores_non_diagnostic_lines() {
		let out = parse(b"", b"some other output\nx -> y\n", 0);
		assert!(out.is_empty());
	}

	#[test]
	fn each_finding_names_its_file() {
		let stderr = b"flake.nix>1:1:W:4:first\nnix/b.nix>2:3:E:0:second\n<stdin>>3:1:I:20:piped\n";
		let files: Vec<_> = parse(b"", stderr, 1).into_iter().map(|d| d.file).collect();
		assert_eq!(
			files,
			[Some("flake.nix".to_string()), Some("nix/b.nix".to_string()), None]
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: b"<stdin>>3:1:W:4:Assignment instead of inherit from\n\
<stdin>>7:5:E:0:Syntax error: unexpected token\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"<stdin>>2:7:I:20:Found repeated keys\n",
		exit: 1,
	},
];
