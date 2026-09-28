//! The MSVC line: `path(line,col): level CODE: message`, as MSVC and
//! `tsc --pretty false` print it.
use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::response::Response;
use crate::severity::{self, Level};

use super::lines;

const LEVELS: &[Level] = &[
	Level("fatal error", severity::ERROR),
	Level("error", severity::ERROR),
	Level("warning", severity::WARNING),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "msvc",
	description: "The MSVC line `path(line,col): error CODE: message`, as MSVC and `tsc --pretty false` \
        print it. Recognized when a line matches.",
	url: "https://learn.microsoft.com/en-us/cpp/build/formatting-the-output-of-a-custom-build-step-or-build-event",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	let diags: Vec<RawDiagnostic> = lines::of(stdout, stderr).iter().filter_map(|l| parse_line(l)).collect();
	if diags.is_empty() {
		return Response::unrecognized(DESCRIPTOR.name);
	}
	Response::recognized(DESCRIPTOR.name, diags)
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	line.match_indices('(').find_map(|(open, _)| at(line, open))
}

/// The finding of `line` if its location's parenthesis opens at `open`.
fn at(line: &str, open: usize) -> Option<RawDiagnostic> {
	if open == 0 {
		return None;
	}
	let (row, col, rest) = position(&line[open + 1..])?;
	let level = LEVELS
		.iter()
		.find(|l| rest.strip_prefix(l.0).is_some_and(|after| after.starts_with(' ')))?;
	let (code, message) = rest[level.0.len() + 1..].split_once(": ")?;
	if code.is_empty() || !code.chars().all(|c| c.is_ascii_alphanumeric() || c == '_') || message.is_empty() {
		return None;
	}
	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity::of(LEVELS, level.0),
		code: Some(code.to_string()),
		file: crate::diagnostic::file_field(&line[..open]),
		..RawDiagnostic::default()
	})
}

/// `line,col): ` at the start of `s`, and what follows it.
fn position(s: &str) -> Option<(u32, u32, &str)> {
	let (inner, rest) = s.split_once("): ")?;
	let (row, col) = inner.split_once(',')?;
	let all_digits = |t: &str| !t.is_empty() && t.bytes().all(|b| b.is_ascii_digit());
	if !all_digits(row) || !all_digits(col) {
		return None;
	}
	Some((row.parse().ok()?, col.parse().ok()?, rest))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn reads_a_tsc_line() {
		let r = parse(
			b"src/a.ts(3,5): error TS2322: Type 'string' is not assignable to type 'number'.\n",
			b"",
			2,
		);
		assert!(r.recognized);
		let d = &r.diagnostics[0];
		assert_eq!(d.file.as_deref(), Some("src/a.ts"));
		assert_eq!((d.row, d.col), (Some(3), Some(5)));
		assert_eq!(d.code.as_deref(), Some("TS2322"));
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.message, "Type 'string' is not assignable to type 'number'.");
	}

	#[test]
	fn keeps_parentheses_and_a_drive_in_the_path() {
		let r = parse(
			br"C:\src\a (copy).cpp(10,5): warning C4996: 'strcpy': This function may be unsafe.",
			b"",
			0,
		);
		let d = &r.diagnostics[0];
		assert_eq!(d.file.as_deref(), Some(r"C:\src\a (copy).cpp"));
		assert_eq!(d.severity, Some(severity::WARNING));
		assert_eq!(d.code.as_deref(), Some("C4996"));
	}

	#[test]
	fn prose_and_continuation_lines_are_no_finding() {
		let r = parse(
			b"  Type 'x' is not assignable.\nFound 1 error (in 2 files).\nf(1,2): note X: y\n",
			b"",
			2,
		);
		assert!(!r.recognized);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"src/a.ts(3,5): error TS2322: Type 'string' is not assignable to type 'number'.\nsrc/b.ts(1,1): error TS1005: ';' expected.\n",
	stderr: b"",
	exit: 2,
}];
