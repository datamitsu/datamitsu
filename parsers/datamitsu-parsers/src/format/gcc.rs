//! The gcc line: `path:line:col: level: message`, and `path:line: message`
//! without a column. gcc and clang print it, and so do many linters asked for a
//! compiler-style line (`typos --format brief`, `shellcheck -f gcc`).
//!
//! The location is the first `:<digits>:` (and optional `<digits>:`) followed by
//! a space, so a path holding a colon — a Windows drive, `weird:name.c` — stays
//! whole. The level word is optional; a message may end in the rule it breaks.
use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::response::Response;
use crate::severity::{self, Level};

use super::lines;

/// The level words gcc prints, which `gccdiag` shares.
pub(crate) const LEVELS: &[Level] = &[
	Level("fatal error", severity::ERROR),
	Level("error", severity::ERROR),
	Level("warning", severity::WARNING),
	Level("note", severity::INFO),
	Level("info", severity::INFO),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "gcc",
	description: "The compiler line `path:line:col: level: message` (and `path:line: message`), as gcc and \
        clang print it and as linters print it on request: `typos --format brief`, `shellcheck -f gcc`, \
        `golangci-lint run --output.text.path=stdout`. A trailing `[rule]` or `(rule)` becomes the code. \
        Recognized when a line matches.",
	url: "https://gcc.gnu.org/onlinedocs/gcc/Diagnostic-Message-Formatting-Options.html",
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

/// The findings of the gcc lines of one stream.
#[cfg(feature = "tools")]
pub(crate) fn findings(stream: &[u8]) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stream)
		.lines()
		.filter_map(|l| parse_line(&lines::strip_ansi(l)))
		.collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let (file, row, col, rest) = location(line)?;
	let (level, message) = match LEVELS
		.iter()
		.find(|l| rest.strip_prefix(l.0).is_some_and(|after| after.starts_with(':')))
	{
		Some(l) => (severity::of(LEVELS, l.0), rest[l.0.len() + 1..].trim_start()),
		None => (None, rest),
	};
	if message.is_empty() {
		return None;
	}
	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col,
		severity: level,
		code: lines::trailing_code(message),
		file: crate::diagnostic::file_field(file),
		..RawDiagnostic::default()
	})
}

/// The path, line, optional column and the text after the location of a gcc
/// line: the first `:<line>:` or `:<line>:<col>:` after a non-empty path that
/// is followed by a space. A path holds no double quote — text in quotes is a
/// string of some other format that mentions a location — and more than
/// digits, colons and spaces, which are a time or a count.
fn location(line: &str) -> Option<(&str, u32, Option<u32>, &str)> {
	let bytes = line.as_bytes();
	for (i, _) in line.match_indices(':') {
		let path = &line[..i];
		if path.contains('"')
			|| !path
				.chars()
				.any(|c| !c.is_ascii_digit() && c != ':' && !c.is_whitespace())
		{
			continue;
		}
		let Some((row, after_row)) = digits(line, i + 1) else {
			continue;
		};
		if bytes.get(after_row) != Some(&b':') {
			continue;
		}
		let (col, end) = match digits(line, after_row + 1) {
			Some((col, after_col)) if bytes.get(after_col) == Some(&b':') => (Some(col), after_col + 1),
			_ => (None, after_row + 1),
		};
		if bytes.get(end) != Some(&b' ') {
			continue;
		}
		return Some((&line[..i], row, col, line[end + 1..].trim_start()));
	}
	None
}

/// The number that starts at `from`, and the index after it.
fn digits(s: &str, from: usize) -> Option<(u32, usize)> {
	let rest = s.get(from..)?;
	let len = rest.bytes().take_while(u8::is_ascii_digit).count();
	if len == 0 {
		return None;
	}
	Some((rest[..len].parse().ok()?, from + len))
}

#[cfg(test)]
mod tests {
	use super::*;

	fn one(line: &str) -> RawDiagnostic {
		parse_line(line).unwrap_or_else(|| panic!("no finding in {line:?}"))
	}

	#[test]
	fn reads_a_compiler_line() {
		let d = one("src/a.c:12:4: warning: unused variable 'x' [-Wunused-variable]");
		assert_eq!(d.file.as_deref(), Some("src/a.c"));
		assert_eq!((d.row, d.col), (Some(12), Some(4)));
		assert_eq!(d.severity, Some(severity::WARNING));
		assert_eq!(d.code.as_deref(), Some("-Wunused-variable"));
		assert_eq!(d.message, "unused variable 'x' [-Wunused-variable]");
	}

	#[test]
	fn a_line_without_a_level_or_a_column() {
		let d = one("README.md:3:7: `teh` -> `the`");
		assert_eq!((d.severity, d.code), (None, None));
		assert_eq!(d.message, "`teh` -> `the`");
		let d = one("a.py:9: missing docstring (D100)");
		assert_eq!((d.row, d.col), (Some(9), None));
		assert_eq!(d.code.as_deref(), Some("D100"));
	}

	#[test]
	fn keeps_a_path_with_a_colon_whole() {
		assert_eq!(one("weird:name.c:12:4: error: x").file.as_deref(), Some("weird:name.c"));
		assert_eq!(one(r"C:\src\a.c:3:5: warning: y").file.as_deref(), Some(r"C:\src\a.c"));
		assert_eq!(one("pkg/a.go:1:2: z: 3:4: w").message, "z: 3:4: w");
	}

	#[test]
	fn a_fatal_error_and_a_word_that_is_no_level() {
		let d = one("a.cpp:1:10: fatal error: missing.h: No such file or directory");
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.message, "missing.h: No such file or directory");
		let d = one("a.c:1:1: remark: something");
		assert_eq!((d.severity, d.message.as_str()), (None, "remark: something"));
	}

	#[test]
	fn prose_is_no_finding() {
		for line in [
			"a.c: In function 'main':",
			"    3 |   return y;",
			"compilation terminated.",
			":1:2: no path",
			"12:30:45: a time",
			"a.c:1:2:no space",
			"a.c:x:y: letters",
			r#"{"msg":"a.c:1:2: error: in a JSON string"}"#,
		] {
			assert!(parse_line(line).is_none(), "{line:?}");
		}
	}

	#[test]
	fn recognizes_a_line_on_either_stream() {
		let r = parse(b"", b"\x1b[1ma.c:1:2: error: x\x1b[0m\n", 1);
		assert!(r.recognized);
		assert_eq!(r.format, "gcc");
		assert_eq!(r.diagnostics[0].file.as_deref(), Some("a.c"));
		assert!(!parse(b"all good\n", b"", 0).recognized);
		assert!(!parse(b"", b"", 0).recognized);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"README.md:3:7: `teh` -> `the`\ndocs/a.md:10:1: `recieve` -> `receive`\n",
		stderr: b"",
		exit: 2,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"a.c:3:10: error: 'y' undeclared\na.c:2:7: warning: unused variable 'x' [-Wunused-variable]\na.c:3:10: note: each undeclared identifier is reported only once\n",
		exit: 1,
	},
];
