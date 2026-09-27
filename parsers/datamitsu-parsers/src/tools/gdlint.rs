//! gdlint — static analysis linter for GDScript. Ported from the none-ls diagnostics/gdlint builtin.
//!
//! gdlint prints `<file>:<line>: Error: <description> (<check>)`. The `Error`
//! word is its level and the parenthesized check name its rule: both are taken
//! out of the message into severity and code.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "gdlint",
	description: "A linter that performs a static analysis on gdscript code according to some predefined configuration.",
	url: "https://github.com/Scony/godot-gdscript-toolkit",
	severities: &[Level("Error", severity::ERROR)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["{file}"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true in the builtin.
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Ported Lua pattern: ":(%d+): (.*)" capturing row and message.
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let colon = line.find(':')?;
	let rest = &line[colon + 1..];
	let next_colon = rest.find(':')?;
	let row: u32 = rest[..next_colon].trim().parse().ok()?;
	let message = rest[next_colon + 1..].trim();

	let (severity, message) = match message
		.split_once(": ")
		.and_then(|(token, text)| Some((severity::of(DESCRIPTOR.severities, token)?, text)))
	{
		Some((level, text)) => (Some(level), text),
		None => (None, message),
	};
	let (message, code) = split_check(message);
	if message.is_empty() {
		return None;
	}
	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		severity,
		code,
		..RawDiagnostic::default()
	})
}

/// Split the trailing `(<check-name>)` gdlint appends to every problem.
fn split_check(message: &str) -> (&str, Option<String>) {
	let check = message
		.strip_suffix(')')
		.and_then(|m| m.rsplit_once(" ("))
		.filter(|(_, name)| {
			!name.is_empty()
				&& name
					.chars()
					.all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '_')
		});
	match check {
		Some((text, name)) => (text.trim_end(), Some(name.to_string())),
		None => (message, None),
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const LINT: &[u8] = b"player.gd:3: Error: Function name \"DoThing\" is not valid (function-name)\nplayer.gd:12: Error: Max allowed line length (100) exceeded (max-line-length)\nFailure: 2 problems found\n";

	#[test]
	fn reads_the_level_and_the_check_gdlint_prints() {
		let diags = parse(b"", LINT, 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].row, Some(3));
		assert_eq!(diags[0].message, "Function name \"DoThing\" is not valid");
		assert_eq!(diags[0].code.as_deref(), Some("function-name"));
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[1].message, "Max allowed line length (100) exceeded");
		assert_eq!(diags[1].code.as_deref(), Some("max-line-length"));
	}

	#[test]
	fn parses_row_and_message() {
		let stderr = b"player.gd:12: Function argument name is not valid\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(12));
		assert_eq!(diags[0].message, "Function argument name is not valid");
		assert_eq!(diags[0].col, None);
		assert_eq!(diags[0].severity, None);
		assert_eq!(diags[0].code, None);
	}

	#[test]
	fn ignores_non_matching_lines() {
		let stderr = b"Success: no problems found\nfoo.gd:3: unused variable\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(3));
		assert_eq!(diags[0].message, "unused variable");
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"",
	stderr: tests::LINT,
	exit: 1,
}];
