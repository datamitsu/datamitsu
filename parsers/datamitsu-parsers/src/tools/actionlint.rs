//! actionlint — static checker for GitHub Actions workflow files.
//! Ported from the none-ls diagnostics/actionlint builtin.
//!
//! actionlint emits a JSON array via `-format '{{json .}}'`. Each object carries
//! `message`, `line`, `column`, `end_column` and `kind` (mapped to `code`). The
//! tool prints no level, so a finding has none. `end_column` is the last column
//! of the underlined span (inclusive), so it gains 1; 0 means unknown.
//! actionlint writes the `-format` JSON report to **stdout** (the none-ls
//! `from_stderr = true` is wrong for this invocation), so read stdout first and
//! fall back to stderr only when stdout is empty.

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "actionlint",
	description: "Actionlint is a static checker for GitHub Actions workflow files.",
	url: "https://github.com/rhysd/actionlint",
	severities: &[],
	column_unit: "utf-8",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-no-color", "-format", "{{json .}}", "-"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// stdout-first: actionlint writes the JSON report to stdout, but fall back to
	// stderr so a future stderr-emitting build still parses.
	let bytes = if stdout.is_empty() { stderr } else { stdout };
	let attrs = Attrs {
		code: "kind",
		end_col: "end_column",
		file: "filepath",
		..Attrs::defaults()
	};
	let mut out = json_diag::from_json(bytes, &attrs, no_level);
	for d in &mut out {
		d.end_col = match (d.col, d.end_col) {
			(Some(col), Some(last)) if last >= col => last.checked_add(1),
			_ => None,
		};
		d.source = Some("actionlint".to_string());
	}
	out
}

fn no_level(_token: &str) -> Option<u8> {
	None
}

#[cfg(test)]
mod tests {
	use super::*;

	const SAMPLE: &[u8] = br#"[{"message":"shellcheck reported issue in this script","filepath":".github/workflows/ci.yaml","line":21,"column":9,"kind":"shellcheck","snippet":"echo hi","end_column":15},{"message":"property \"foo\" is not defined","filepath":".github/workflows/ci.yaml","line":3,"column":5,"kind":"expression"}]"#;

	#[test]
	fn parses_actionlint_json_from_stdout() {
		// The report arrives on stdout, the stream this tool actually writes to.
		let out = parse(SAMPLE, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "shellcheck reported issue in this script");
		assert_eq!(out[0].row, Some(21));
		assert_eq!(out[0].col, Some(9));
		assert_eq!(out[0].end_col, Some(16));
		assert_eq!(out[0].code.as_deref(), Some("shellcheck"));
		assert_eq!(out[0].source.as_deref(), Some("actionlint"));
		assert_eq!(out[1].code.as_deref(), Some("expression"));
		assert_eq!(out[1].end_col, None);
	}

	#[test]
	fn never_sets_a_severity() {
		assert!(parse(SAMPLE, b"", 1).iter().all(|d| d.severity.is_none()));
	}

	#[test]
	fn an_unknown_end_column_is_dropped() {
		let out = parse(
			br#"[{"message":"m","line":1,"column":4,"kind":"k","end_column":0}]"#,
			b"",
			1,
		);
		assert_eq!(out[0].end_col, None);
	}

	#[test]
	fn falls_back_to_stderr_when_stdout_empty() {
		assert_eq!(parse(b"", SAMPLE, 1).len(), 2);
	}

	#[test]
	fn empty_output_yields_nothing() {
		assert!(parse(b"", b"", 0).is_empty());
	}
	#[test]
	fn names_the_workflow_file_but_not_stdin() {
		let json = br#"[{"message":"m","filepath":".github/workflows/a.yaml","line":1,"column":1,"kind":"k"},{"message":"n","filepath":"<stdin>","line":2,"column":1,"kind":"k"}]"#;
		let files: Vec<_> = parse(json, b"", 1).into_iter().map(|d| d.file).collect();
		assert_eq!(files, [Some(".github/workflows/a.yaml".to_string()), None]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"[{"message":"job \"build\" needs job \"missing\" which does not exist in this workflow","filepath":".github/workflows/ci.yml","line":4,"column":3,"kind":"job-needs","snippet":"  build:\n  ^~~~~~","end_column":8},{"message":"property \"foo\" is not defined in object type {}","filepath":".github/workflows/ci.yml","line":9,"column":23,"kind":"expression","snippet":"      - run: echo ${{ env.foo }}\n                      ^~~~~~","end_column":29}]"#,
	stderr: b"",
	exit: 1,
}];
