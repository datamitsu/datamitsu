//! buf — Protocol Buffers linter (`buf lint`).
//!
//! Ported from the none-ls `diagnostics/buf` builtin. Diagnostics come from
//! **stderr** (`from_stderr = true`); each line matches the Lua pattern
//! `(.*):(%d+):(%d+):(.*)` with groups filename, row, col, message:
//!
//! ```text
//! <file>:<row>:<col>:<message>
//! ```
//!
//! The tool emits no level or rule code, so severity/code stay `None`.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "buf",
	description: "A new way of working with Protocol Buffers.",
	url: "https://github.com/bufbuild/buf",
	operations: &[Operation {
		mode: "lint",
		args: &["lint", "{file}#include_package_files=true"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Pattern `(.*):(%d+):(%d+):(.*)`: filename is greedy, so row/col are the two
	// numeric `:`-fields immediately before the message. Scan from the left to
	// find "<file>:<digits>:<digits>:", keeping the longest filename prefix.
	let mut search_from = 0;
	loop {
		let rel = line[search_from..].find(':')?;
		let c1 = search_from + rel;
		let after = &line[c1 + 1..];
		let c2_rel = after.find(':')?;
		let c2 = c1 + 1 + c2_rel;
		let row_str = &line[c1 + 1..c2];
		let rest = &line[c2 + 1..];
		let c3_rel = rest.find(':')?;
		let c3 = c2 + 1 + c3_rel;
		let col_str = &line[c2 + 1..c3];

		if let (Ok(row), Ok(col)) = (row_str.parse::<u32>(), col_str.parse::<u32>()) {
			let message = line[c3 + 1..].to_string();
			return Some(RawDiagnostic {
				message,
				row: Some(row),
				col: Some(col),
				..RawDiagnostic::default()
			});
		}
		search_from = c1 + 1;
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_basic_diagnostic() {
		let d = parse_line("foo.proto:3:1:Field name \"Foo\" should be lower_snake_case.").unwrap();
		assert_eq!(d.row, Some(3));
		assert_eq!(d.col, Some(1));
		assert_eq!(d.message, "Field name \"Foo\" should be lower_snake_case.");
		assert_eq!(d.severity, None);
		assert_eq!(d.code, None);
	}

	#[test]
	fn message_with_colons_is_kept() {
		let d = parse_line("a/b.proto:10:5:Import \"x:y\" not found.").unwrap();
		assert_eq!((d.row, d.col), (Some(10), Some(5)));
		assert_eq!(d.message, "Import \"x:y\" not found.");
	}

	#[test]
	fn parse_reads_stderr_and_skips_noise() {
		let out = parse(
			b"",
			b"foo.proto:1:1:problem one\nFailure: something\nbar.proto:2:3:problem two\n",
			1,
		);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "problem one");
		assert_eq!(out[1].row, Some(2));
	}
}
