use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "ty",
	description: "Python type diagnostics from ty's concise reporter.",
	url: "https://docs.astral.sh/ty/",
	operations: &[Operation {
		mode: "lint",
		args: &["check", "--output-format", "concise", "--color", "never", "{files}"],
		stdin: false,
	}],
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("info", severity::INFO),
	],
	column_unit: "utf-32",
	category: "",
	kind: "tool",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	[stdout, stderr]
		.iter()
		.flat_map(|stream| {
			String::from_utf8_lossy(stream)
				.lines()
				.filter_map(parse_line)
				.collect::<Vec<_>>()
		})
		.collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let (location, report) = line.split_once(": ")?;
	let (file, row, col) = crate::location::file_row_col(location)?;
	let (level, rest) = report.split_once('[')?;
	let (code, message) = rest.split_once("] ")?;
	if code.is_empty() || message.is_empty() {
		return None;
	}
	Some(RawDiagnostic {
		message: message.into(),
		file: crate::diagnostic::file_field(file),
		row: Some(row),
		col: Some(col),
		code: Some(code.into()),
		severity: severity::of(DESCRIPTOR.severities, level),
		source: Some(DESCRIPTOR.name.into()),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn keeps_windows_paths_rules_and_levels_in_both_streams() {
		let out = parse(
			b"C:\\project\\main.py:2:8: error[invalid-assignment] Wrong type\n",
			b"src/other.py:3:4: warning[unused-ignore-comment] Unused suppression\nFound 2 diagnostics\n",
			1,
		);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].file.as_deref(), Some("C:\\project\\main.py"));
		assert_eq!((out[0].row, out[0].col), (Some(2), Some(8)));
		assert_eq!(out[0].code.as_deref(), Some("invalid-assignment"));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].source.as_deref(), Some("ty"));
	}

	#[test]
	fn unrelated_output_is_not_a_clean_failed_run() {
		assert!(
			!crate::tools::answer("ty", b"broken reporter", b"", 1)
				.unwrap()
				.recognized
		);
		assert!(
			crate::tools::answer("ty", b"All checks passed!\n", b"", 0)
				.unwrap()
				.recognized
		);
	}
}

#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"main.py:1:14: error[invalid-assignment] Wrong type\n",
	stderr: b"",
	exit: 1,
}];
