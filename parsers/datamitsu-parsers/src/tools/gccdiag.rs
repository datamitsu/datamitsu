//! gccdiag — wrapper for any C/C++ compiler that uses correct args from
//! compile_commands.json. Ported from the none-ls diagnostics/gccdiag builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "gccdiag",
    description: "gccdiag is a wrapper for any C/C++ compiler (gcc, avr-gcc, arm-none-eabi-gcc, etc) that automatically uses the correct compiler arguments for a file in your project by parsing the `compile_commands.json` file at the root of your project.",
    url: "https://gitlab.com/andrejr/gccdiag",
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
	let (_filename, rest) = line.split_once(':')?;
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
		severity: severity_of(sev_s),
		..RawDiagnostic::default()
	})
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"fatal error" | "error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		"note" => Some(severity::INFO),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_and_warning() {
		let stderr = b"main.c:10:5: error: 'foo' undeclared (first use in this function)\nmain.c:12:9: warning: unused variable 'x' [-Wunused-variable]\n";
		let diags = parse(&[], stderr, 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].row, Some(10));
		assert_eq!(diags[0].col, Some(5));
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[0].message, "'foo' undeclared (first use in this function)");
		assert_eq!(diags[1].severity, Some(severity::WARNING));
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
	fn ignores_non_diagnostic_lines() {
		let stderr = b"some unrelated build chatter\n";
		assert!(parse(&[], stderr, 1).is_empty());
	}
}
