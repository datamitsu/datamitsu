use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "ktfmt",
	description: "Kotlin formatting differences and syntax errors from ktfmt's dry run.",
	url: "https://github.com/facebook/ktfmt",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"--google-style",
			"--enable-editorconfig",
			"--dry-run",
			"--set-exit-if-changed",
			"{files}",
		],
		stdin: false,
	}],
	severities: &[Level("error", severity::ERROR)],
	column_unit: "utf-16",
	category: "",
	kind: "tool",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let mut out = Vec::new();
	for (bytes, paths) in [(stdout, true), (stderr, false)] {
		for line in String::from_utf8_lossy(bytes).lines() {
			let line = line.trim();
			if let Some((location, message)) = line.split_once(": error: ") {
				let (file, row, col) = match crate::location::file_row_col(location) {
					Some((file, row, col)) => (file, Some(row), Some(col)),
					None => (location, None, None),
				};
				if kotlin_path(file) && !message.is_empty() {
					out.push(RawDiagnostic {
						message: message.to_string(),
						file: crate::diagnostic::file_field(file),
						row,
						col,
						severity: severity::of(DESCRIPTOR.severities, "error"),
						source: Some(DESCRIPTOR.name.to_string()),
						..RawDiagnostic::default()
					});
				}
			} else if paths && kotlin_path(line) {
				// A dry-run difference is only a path: ktfmt supplies no message or position.
				out.push(RawDiagnostic {
					message: line.to_string(),
					file: crate::diagnostic::file_field(line),
					source: Some(DESCRIPTOR.name.to_string()),
					..RawDiagnostic::default()
				});
			}
		}
	}
	out
}

fn kotlin_path(path: &str) -> bool {
	path.ends_with(".kt") || path.ends_with(".kts")
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn differences_keep_paths_without_inventing_positions_or_levels() {
		let out = parse(b"src/With spaces.kt\r\nC:\\project\\build.gradle.kts\n", b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].file.as_deref(), Some("src/With spaces.kt"));
		assert_eq!(out[1].file.as_deref(), Some("C:\\project\\build.gradle.kts"));
		assert!(out
			.iter()
			.all(|d| d.row.is_none() && d.col.is_none() && d.severity.is_none() && d.code.is_none()));
	}

	#[test]
	fn syntax_errors_keep_utf16_coordinates_and_the_printed_level() {
		let out = parse(b"", b"C:\\project\\Broken.kt:1:46: error: Expecting an expression\n", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].file.as_deref(), Some("C:\\project\\Broken.kt"));
		assert_eq!((out[0].row, out[0].col), (Some(1), Some(46)));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].message, "Expecting an expression");
		assert_eq!(out[0].source.as_deref(), Some("ktfmt"));
	}

	#[test]
	fn reads_both_streams_and_ignores_success_and_jvm_noise() {
		let out = parse(
			b"src/Main.kt\n",
			b"WARNING: Deprecated JVM method\nDone formatting src/Main.kt\nsrc/Broken.kt:2:3: error: Unexpected tokens\n",
			1,
		);
		assert_eq!(out.len(), 2);
		assert!(parse(b"", b"Done formatting src/Main.kt\n", 0).is_empty());
		assert!(parse(b"", b"unrelated.kt\n", 1).is_empty());
	}

	#[test]
	fn errors_without_positions_do_not_receive_synthetic_coordinates() {
		let out = parse(b"", b"src/Missing.kt: error: Could not read file\n", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, None);
		assert_eq!(out[0].col, None);
	}
}

#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"src/Main.kt\n",
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"src/Broken.kt:1:46: error: Expecting an expression\n",
		exit: 1,
	},
];
