//! cppcheck — a tool for fast static analysis of C/C++ code.
//!
//! Ported from the none-ls `diagnostics/cppcheck` builtin. It runs cppcheck with
//! `--template=gcc`, reads from **stderr**, and each diagnostic line looks like:
//!
//! ```text
//! <file>:<row>:<col>: <severity>: <message> [<id>]
//! ```
//!
//! e.g. `main.c:5:7: error: Array 'a[10]' accessed at index 10, which is out of bounds. [arrayIndexOutOfBounds]`
//!
//! The none-ls Lua pattern `(%d+):(%d+): (%w+): (.*)` matches the `row:col: sev: msg`
//! tail anywhere in the line (so a path with colons doesn't break parsing). The
//! severity word is read through the vocabulary; a word it does not list sets no
//! level. The trailing `[<id>]` is the check. Positions are 1-based, with no end.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "cppcheck",
	description: "A tool for fast static analysis of C/C++ code.",
	url: "https://github.com/danmar/cppcheck",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("performance", severity::WARNING),
		Level("note", severity::INFO),
		Level("portability", severity::INFO),
		Level("information", severity::INFO),
		Level("style", severity::HINT),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"--enable=warning,style,performance,portability",
			"--template=gcc",
			"{file}",
		],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Find the "row:col: severity: " tail. Scan colon positions and try to match
	// `<digits>:<digits>: <word>: <message>` starting after each "<digits>:<digits>".
	let bytes = line.as_bytes();
	let mut search_from = 0;
	while let Some(rel) = line[search_from..].find(':') {
		let first_colon = search_from + rel;
		// row digits end at first_colon; find their start (contiguous digits).
		let mut row_start = first_colon;
		while row_start > 0 && bytes[row_start - 1].is_ascii_digit() {
			row_start -= 1;
		}
		if row_start < first_colon {
			if let Some(diag) = try_match(line, row_start, first_colon) {
				return Some(diag);
			}
		}
		search_from = first_colon + 1;
	}
	None
}

/// Try to parse `<row>:<col>: <severity>: <message>` where `line[row_start..first_colon]`
/// is the row digit run and `first_colon` is the colon after the row.
fn try_match(line: &str, row_start: usize, first_colon: usize) -> Option<RawDiagnostic> {
	let row: u32 = line[row_start..first_colon].parse().ok()?;

	// col digits immediately after the first colon.
	let rest = &line[first_colon + 1..];
	let col_len = rest.bytes().take_while(|b| b.is_ascii_digit()).count();
	if col_len == 0 {
		return None;
	}
	let col: u32 = rest[..col_len].parse().ok()?;
	let rest = &rest[col_len..];

	// ": " then severity word, then ": " then message.
	let rest = rest.strip_prefix(": ")?;
	let sev_len = rest
		.bytes()
		.take_while(|b| b.is_ascii_alphanumeric() || *b == b'_')
		.count();
	if sev_len == 0 {
		return None;
	}
	let severity_token = &rest[..sev_len];
	let rest = &rest[sev_len..];
	let message = rest.strip_prefix(": ")?;
	let (message, code) = split_id(message);

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity::of(DESCRIPTOR.severities, severity_token),
		code,
		..RawDiagnostic::default()
	})
}

/// Split the template's trailing ` [<id>]` off the message.
fn split_id(message: &str) -> (&str, Option<String>) {
	let Some(open) = message.strip_suffix(']').and_then(|m| m.rfind(" [")) else {
		return (message, None);
	};
	let id = &message[open + 2..message.len() - 1];
	let is_id = !id.is_empty()
		&& id
			.bytes()
			.all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-' || b == b'.');
	if is_id {
		(message[..open].trim_end(), Some(id.to_string()))
	} else {
		(message, None)
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_with_path_containing_colons() {
		let d = parse_line("src/main.c:5:7: error: Array 'a[10]' accessed at index 10, which is out of bounds.").unwrap();
		assert_eq!(d.row, Some(5));
		assert_eq!(d.col, Some(7));
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.message, "Array 'a[10]' accessed at index 10, which is out of bounds.");
		assert_eq!(d.code, None);
	}

	#[test]
	fn reads_the_trailing_id_as_the_code() {
		let d = parse_line(
			"src/main.c:5:7: warning: Array 'a[10]' accessed at index 10, which is out of bounds. [arrayIndexOutOfBounds]",
		)
		.unwrap();
		assert_eq!(d.code.as_deref(), Some("arrayIndexOutOfBounds"));
		assert_eq!(d.message, "Array 'a[10]' accessed at index 10, which is out of bounds.");
		assert_eq!(d.severity, Some(severity::WARNING));
	}

	#[test]
	fn maps_style_to_hint_and_portability_to_info() {
		let style = parse_line("a.cpp:1:1: style: The scope of the variable 'i' can be reduced.").unwrap();
		assert_eq!(style.severity, Some(severity::HINT));

		let port = parse_line("a.cpp:2:3: portability: Non reentrant function used.").unwrap();
		assert_eq!(port.severity, Some(severity::INFO));
	}

	#[test]
	fn an_unknown_severity_word_sets_no_level() {
		let d = parse_line("a.cpp:3:1: debug: ValueFlow bailout").unwrap();
		assert_eq!(d.severity, None);
	}

	#[test]
	fn parse_reads_stderr_and_collects() {
		let stderr = b"x.c:10:2: warning: Possible null pointer dereference.\nx.c:11:4: performance: Prefer prefix.\n";
		let out = parse(b"", stderr, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].row, Some(11));
	}

	#[test]
	fn non_matching_line_is_skipped() {
		assert!(parse_line("Checking src/main.c ...").is_none());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"Checking src/main.c ...\n",
		stderr: b"src/main.c:5:7: warning: Array 'a[10]' accessed at index 10, which is out of bounds. [arrayIndexOutOfBounds]\n    a[10] = 0;\n     ^\nsrc/main.c:3:9: note: Assignment 'p=0', assigned value is 0\n    p = 0;\n        ^\n",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: b"Checking a.cpp ...\n",
		stderr: b"a.cpp:1:10: style: The scope of the variable 'i' can be reduced. [variableScope]\na.cpp:2:3: portability: Non reentrant function 'localtime' called. [localtimeCalled]\na.cpp:4:5: error: Null pointer dereference: p [nullPointer]\n",
		exit: 1,
	},
];
