//! qmllint — verifies the syntactic validity of QML files. Ported from the
//! none-ls diagnostics/qmllint builtin.
//!
//! Since Qt 6.4 a message ends with its warning category in brackets
//! (`Unqualified access [unqualified]`); that is the rule id.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::location;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "qmllint",
	description: "qmllint is a tool shipped with Qt that verifies the syntactic validity of QML files. It also warns about some QML anti-patterns.",
	url: "https://doc-snapshots.qt.io/qt6-dev/qtquick-tools-and-utilities.html#qmllint",
	severities: &[Level("Error", severity::ERROR), Level("Warning", severity::WARNING)],
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
	// Diagnostics are emitted on stderr (from_stderr = true).
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Errorformat layout from the builtin:
//   "%trror: %f:%l:%c: %m"   -> "Error: <file>:<line>:<col>: <message>"
//   "%tarning: %f:%l:%c: %m" -> "Warning: <file>:<line>:<col>: <message>"
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let (level, rest) = line.split_once(": ")?;
	// Only a line headed by a level word is a finding.
	let severity = severity::of(DESCRIPTOR.severities, level)?;
	let (loc, message) = rest.split_once(": ")?;
	let (_file, row, col) = location::file_row_col(loc)?;
	let (message, code) = split_category(message);

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: Some(severity),
		code: code.map(str::to_string),
		..RawDiagnostic::default()
	})
}

/// Split a trailing ` [category]` off the message.
fn split_category(message: &str) -> (&str, Option<&str>) {
	match message.strip_suffix(']').and_then(|m| m.rsplit_once(" [")) {
		Some((text, id)) if is_category(id) => (text, Some(id)),
		_ => (message, None),
	}
}

fn is_category(id: &str) -> bool {
	!id.is_empty()
		&& id
			.bytes()
			.all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b'-'))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_and_warning() {
		let stderr =
			b"Error: /path/to/Main.qml:10:5: Unexpected token\nWarning: /path/to/Main.qml:12:3: Unqualified access\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 2);

		assert_eq!(diags[0].message, "Unexpected token");
		assert_eq!(diags[0].row, Some(10));
		assert_eq!(diags[0].col, Some(5));
		assert_eq!(diags[0].end_col, None);
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[0].code, None);

		assert_eq!(diags[1].message, "Unqualified access");
		assert_eq!(diags[1].row, Some(12));
		assert_eq!(diags[1].col, Some(3));
		assert_eq!(diags[1].severity, Some(severity::WARNING));
	}

	#[test]
	fn the_bracketed_category_is_the_code() {
		let diags = parse(b"", SAMPLES[0].stderr, SAMPLES[0].exit);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].message, "Unqualified access");
		assert_eq!(diags[0].code.as_deref(), Some("unqualified"));
		assert_eq!((diags[0].row, diags[0].col), (Some(12), Some(19)));
		assert_eq!(diags[1].code.as_deref(), Some("unused-imports"));
		assert_eq!(diags[1].severity, Some(severity::WARNING));
	}

	#[test]
	fn a_bracket_with_spaces_stays_in_the_message() {
		let diags = parse(b"", b"Warning: a.qml:1:1: Type [not a category]\n", 255);
		assert_eq!(diags[0].message, "Type [not a category]");
		assert_eq!(diags[0].code, None);
	}

	#[test]
	fn ignores_unmatched_lines() {
		let stderr = b"some unrelated output\nInfo: a.qml:2:1: a suggestion\nError: a.qml:1:1: boom\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].message, "boom");
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: br#"Warning: /src/Main.qml:12:19: Unqualified access [unqualified]
            text: count
                  ^^^^^
Info: count is a member of a parent element.
      You can qualify the access with its id to avoid this warning.
Warning: /src/Main.qml:3:1: Unused import [unused-imports]
import QtQuick.Controls
^^^^^^
"#,
		exit: 255,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"Error: /src/Broken.qml:10:5: Expected token `}'\n",
		exit: 255,
	},
];
