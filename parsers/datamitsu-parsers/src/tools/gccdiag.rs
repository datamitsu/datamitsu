//! gccdiag — wrapper for any C/C++ compiler that uses correct args from
//! compile_commands.json. Ported from the none-ls diagnostics/gccdiag builtin.
//!
//! The warning option GCC ends a diagnostic with (`[-Wunused-variable]`) is the
//! rule, and becomes the code.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "gccdiag",
	description: "gccdiag is a wrapper for any C/C++ compiler (gcc, avr-gcc, arm-none-eabi-gcc, etc) that automatically uses the correct compiler arguments for a file in your project by parsing the `compile_commands.json` file at the root of your project.",
	url: "https://gitlab.com/andrejr/gccdiag",
	severities: &[
		Level("fatal error", severity::ERROR),
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("note", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"--default-args",
			"-S -x $FILEEXT",
			"-i",
			"-fdiagnostics-color",
			"--",
			"{file}",
		],
		stdin: false,
	}],
};

// from_stderr = true: diagnostics arrive on stderr.
pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Pattern: ^([^:]+):(%d+):(%d+):%s+([^:]+):%s+(.*)$
// fields: filename, row, col, severity, message
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// filename: up to first ':'
	let (filename, rest) = line.split_once(':')?;
	// row: digits up to ':'
	let (row_s, rest) = rest.split_once(':')?;
	let row: u32 = row_s.parse().ok()?;
	// col: digits up to ':'
	let (col_s, rest) = rest.split_once(':')?;
	let col: u32 = col_s.parse().ok()?;
	// severity: %s+ then [^:]+ up to ':'
	let (sev_s, message) = rest.split_once(':')?;
	let sev_s = sev_s.trim();
	// message: %s+(.*)
	let message = message.trim_start();
	if message.is_empty() {
		return None;
	}

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity::of(DESCRIPTOR.severities, sev_s),
		code: warning_option(message),
		file: crate::diagnostic::file_field(filename),
		..RawDiagnostic::default()
	})
}

/// The trailing `[-W…]` option, named as the rule it enables: `-Werror=` only
/// says that the rule was promoted to an error.
fn warning_option(message: &str) -> Option<String> {
	let open = message.rfind(" [-W")?;
	let option = message[open + 2..].strip_suffix(']')?;
	if option.contains([' ', '[', ']']) {
		return None;
	}
	Some(match option.strip_prefix("-Werror=") {
		Some(rule) => format!("-W{rule}"),
		None => option.to_string(),
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const COMPILE: &[u8] = b"a.c: In function \xe2\x80\x98main\xe2\x80\x99:\na.c:3:10: error: \xe2\x80\x98y\xe2\x80\x99 undeclared (first use in this function)\n    3 |   return y;\n      |          ^\na.c:3:10: note: each undeclared identifier is reported only once for each function it appears in\na.c:2:7: warning: unused variable \xe2\x80\x98x\xe2\x80\x99 [-Wunused-variable]\n    2 |   int x;\n      |       ^\n";

	#[test]
	fn parses_error_and_warning() {
		let stderr = b"main.c:10:5: error: 'foo' undeclared (first use in this function)\nmain.c:12:9: warning: unused variable 'x' [-Wunused-variable]\n";
		let diags = parse(&[], stderr, 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].row, Some(10));
		assert_eq!(diags[0].col, Some(5));
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[0].message, "'foo' undeclared (first use in this function)");
		assert_eq!(diags[0].code, None);
		assert_eq!(diags[1].severity, Some(severity::WARNING));
		assert_eq!(diags[1].code.as_deref(), Some("-Wunused-variable"));
	}

	#[test]
	fn parses_fatal_error_and_note() {
		let stderr =
			b"a.cpp:1:10: fatal error: missing.h: No such file or directory\na.cpp:3:1: note: in expansion of macro 'BAR'\n";
		let diags = parse(&[], stderr, 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[1].severity, Some(severity::INFO));
		assert_eq!(diags[1].message, "in expansion of macro 'BAR'");
	}

	#[test]
	fn reads_a_gcc_run_with_its_source_excerpts() {
		let diags = parse(&[], COMPILE, 1);
		assert_eq!(diags.len(), 3);
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[1].severity, Some(severity::INFO));
		assert_eq!((diags[2].row, diags[2].col), (Some(2), Some(7)));
		assert_eq!(diags[2].code.as_deref(), Some("-Wunused-variable"));
	}

	#[test]
	fn names_a_promoted_warning_by_its_own_option() {
		let stderr = b"c.c:1:19: error: unused variable 'unused' [-Werror=unused-variable]\n";
		let d = &parse(&[], stderr, 1)[0];
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.code.as_deref(), Some("-Wunused-variable"));
	}

	#[test]
	fn a_level_gcc_does_not_print_is_left_unset() {
		let d = &parse(&[], b"a.c:1:1: remark: something\n", 0)[0];
		assert_eq!(d.severity, None);
	}

	#[test]
	fn ignores_non_diagnostic_lines() {
		let stderr = b"some unrelated build chatter\n";
		assert!(parse(&[], stderr, 1).is_empty());
	}

	#[test]
	fn each_finding_names_its_file() {
		let stderr = b"src/a.c:1:1: error: first\ninclude/b.h:2:3: note: second\n";
		let files: Vec<_> = parse(&[], stderr, 1).into_iter().map(|d| d.file).collect();
		assert_eq!(files, [Some("src/a.c".to_string()), Some("include/b.h".to_string())]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: tests::COMPILE,
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"b.c:1:10: fatal error: missing.h: No such file or directory\n    1 | #include \"missing.h\"\n      |          ^~~~~~~~~~~\ncompilation terminated.\n",
		exit: 1,
	},
];
