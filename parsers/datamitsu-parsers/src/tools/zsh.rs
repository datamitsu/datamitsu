//! zsh — evaluates (but does not execute) zsh scripts via `zsh -n`. Ported from the none-ls diagnostics/zsh builtin.
//!
//! zsh prints `<file>:<row>: <message>` on stderr and no level. A message that
//! opens with `parse error` names one, so that phrase is the level token; any
//! other message has no level.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "zsh",
	description: "Uses zsh's own -n option to evaluate, but not execute, zsh scripts. Effectively, this acts somewhat like a linter, although it only really checks for serious errors - and will likely only show the first error.",
	url: "https://www.zsh.org/",
	severities: &[Level("parse error", severity::ERROR)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-n", "{file}"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Pattern: (.+):(%d+): (.+) -> filename, row, message
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Find the ":<digits>: " separator after the filename.
	// Filename may contain colons, so locate the first ":<digits>:" group.
	let bytes = line.as_bytes();
	let mut search_from = 0;
	loop {
		let rel = line[search_from..].find(':')?;
		let colon = search_from + rel;
		// Parse digits after this colon.
		let mut end = colon + 1;
		while end < bytes.len() && bytes[end].is_ascii_digit() {
			end += 1;
		}
		let has_digits = end > colon + 1;
		if has_digits && end < bytes.len() && bytes[end] == b':' {
			let row: u32 = line[colon + 1..end].parse().ok()?;
			// Expect ": " then message.
			let rest = &line[end + 1..];
			let message = rest.strip_prefix(' ').unwrap_or(rest);
			if message.is_empty() {
				return None;
			}
			return Some(RawDiagnostic {
				message: message.to_string(),
				row: Some(row),
				severity: severity_of(message),
				// A script read from stdin is named after the shell itself.
				file: crate::diagnostic::file_field(&line[..colon]).filter(|f| f != "zsh"),
				..RawDiagnostic::default()
			});
		}
		search_from = colon + 1;
		if search_from >= line.len() {
			return None;
		}
	}
}

/// The level of the vocabulary token the message opens with, as a whole word.
fn severity_of(message: &str) -> Option<u8> {
	let token = DESCRIPTOR.severities.iter().map(|l| l.0).find(|token| {
		message
			.strip_prefix(token)
			.is_some_and(|rest| !rest.starts_with(|c: char| c.is_alphanumeric() || c == '_'))
	})?;
	severity::of(DESCRIPTOR.severities, token)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_line() {
		let stderr = b"script.zsh:5: parse error near `done'\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].message, "parse error near `done'");
		assert_eq!(diags[0].row, Some(5));
	}

	#[test]
	fn a_parse_error_is_an_error() {
		for message in [
			"parse error near `done'",
			"parse error",
			"parse error: condition expected: 1",
		] {
			let line = format!("script.zsh:5: {message}");
			assert_eq!(parse_line(&line).unwrap().severity, Some(severity::ERROR), "{message}");
		}
	}

	#[test]
	fn other_messages_have_no_level() {
		for message in ["missing end of string", "parse errors galore", "unmatched '"] {
			let line = format!("file.zsh:12: {message}");
			assert_eq!(parse_line(&line).unwrap().severity, None, "{message}");
		}
	}

	#[test]
	fn ignores_non_matching_lines() {
		let stderr = b"some unrelated output\nfile.zsh:12: missing end of string\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(12));
		assert_eq!(diags[0].message, "missing end of string");
	}

	#[test]
	fn each_finding_names_its_file() {
		let stderr = b"script.zsh:5: parse error near `done'\nlib/b.zsh:6: unmatched '\nzsh:7: parse error\n";
		let files: Vec<_> = parse(b"", stderr, 1).into_iter().map(|d| d.file).collect();
		assert_eq!(
			files,
			[Some("script.zsh".to_string()), Some("lib/b.zsh".to_string()), None]
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: b"script.zsh:5: parse error near `done'\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"script.zsh:12: unmatched '\n",
		exit: 1,
	},
];
