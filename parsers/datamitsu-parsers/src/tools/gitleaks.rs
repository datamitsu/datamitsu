//! gitleaks — secret-scanning SAST diagnostics. Ported from the none-ls
//! diagnostics/gitleaks builtin.
//!
//! gitleaks emits a JSON array of findings with PascalCase fields
//! (`Description`, `RuleID`, `StartLine`, `StartColumn`, `EndLine`, `EndColumn`).
//! The none-ls builtin remaps those onto its intermediate shape and runs them
//! through `from_json`; here we map them directly via overridden `Attrs`. With
//! `--report-path -`, gitleaks writes the JSON report to **stdout** (its INF/WRN
//! logs go to stderr) — the none-ls `from_stderr = true` is wrong — so we read
//! stdout first and fall back to stderr. Source is the static label "gitleaks";
//! gitleaks emits no severity token.
//!
//! Its columns name the first and last character of the secret, counted within
//! the fragment gitleaks scanned (about 100 KB of a file, or a diff hunk) from
//! the newline before the line — except on the fragment's first line, which has
//! none (gitleaks 8.30.1 prints `StartColumn` 8, `EndColumn` 27 for a
//! 20-character secret at column 7 of line 3, and 15/34 for one at column 15 of
//! line 1). Lines are absolute, so only line 1 is known to open a fragment: its
//! columns are kept, the end made exclusive; any later line keeps its row and
//! drops columns the report cannot pin down.

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "gitleaks",
	description: "Gitleaks is a SAST tool for detecting and preventing hardcoded secrets like passwords, API keys, and tokens in git repos.",
	url: "https://github.com/gitleaks/gitleaks",
	severities: &[],
	column_unit: "",
	category: "security",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"stdin",
			"--report-format",
			"json",
			"--report-path",
			"-",
			"--exit-code",
			"0",
			"--no-banner",
		],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// stdout-first: `--report-path -` writes the JSON findings array to stdout;
	// fall back to stderr so a future stderr-emitting build still parses.
	let bytes = if stdout.is_empty() { stderr } else { stdout };
	let attrs = Attrs {
		row: "StartLine",
		col: "StartColumn",
		end_row: "EndLine",
		end_col: "EndColumn",
		code: "RuleID",
		message: "Description",
		file: "File",
		..Attrs::defaults()
	};
	let mut diags = json_diag::from_json(bytes, &attrs, |_| None);
	for d in &mut diags {
		d.source = Some("gitleaks".to_string());
		match (d.row, d.end_row) {
			(Some(1), None | Some(1)) => d.end_col = d.end_col.and_then(|c| c.checked_add(1)),
			// The end sits on a later line of the fragment line 1 opened: counted
			// from its newline, one too high, which makes it exclusive already.
			(Some(1), Some(_)) => {}
			_ => {
				d.col = None;
				d.end_col = None;
			}
		}
	}
	diags
}

#[cfg(test)]
mod tests {
	use super::*;

	// `gitleaks stdin --report-format json --report-path - --exit-code 0` (8.30.1)
	// over two 20-character AWS keys, one at column 15 of line 1 and one at column
	// 7 of line 3; the keys are redacted here, everything else is verbatim.
	pub(super) const REPORT: &[u8] = br#"[
 {
  "RuleID": "aws-access-token",
  "Description": "Identified a pattern that may indicate AWS credentials, risking unauthorized cloud resource access and data breaches on AWS platforms.",
  "StartLine": 1,
  "EndLine": 1,
  "StartColumn": 15,
  "EndColumn": 34,
  "Match": "REDACTED-FAKE-TEST-SECRET",
  "Secret": "REDACTED-FAKE-TEST-SECRET",
  "File": "",
  "SymlinkFile": "",
  "Commit": "",
  "Entropy": 3.0841837,
  "Author": "",
  "Email": "",
  "Date": "",
  "Message": "",
  "Tags": [],
  "Fingerprint": ":aws-access-token:1"
 },
 {
  "RuleID": "aws-access-token",
  "Description": "Identified a pattern that may indicate AWS credentials, risking unauthorized cloud resource access and data breaches on AWS platforms.",
  "StartLine": 3,
  "EndLine": 3,
  "StartColumn": 8,
  "EndColumn": 27,
  "Match": "REDACTED-FAKE-TEST-SECRET",
  "Secret": "REDACTED-FAKE-TEST-SECRET",
  "File": "",
  "SymlinkFile": "",
  "Commit": "",
  "Entropy": 3.5841837,
  "Author": "",
  "Email": "",
  "Date": "",
  "Message": "",
  "Tags": [],
  "Fingerprint": ":aws-access-token:3"
 }
]"#;

	#[test]
	fn parses_findings_from_stdout() {
		// `--report-path -` writes the findings to stdout (stderr carries logs).
		let out = parse(REPORT, b"", 0);
		assert_eq!(out.len(), 2);
		assert!(out[0]
			.message
			.starts_with("Identified a pattern that may indicate AWS credentials"));
		assert_eq!(out[0].code.as_deref(), Some("aws-access-token"));
		assert_eq!(out[0].source.as_deref(), Some("gitleaks"));
		assert_eq!(out[0].severity, None);
		assert_eq!(out[0].file, None);
	}

	#[test]
	fn spans_the_secret_with_an_exclusive_end() {
		let out = parse(REPORT, b"", 0);
		// Line 1: the secret's first and last column, the last made exclusive.
		assert_eq!(
			(out[0].row, out[0].col, out[0].end_row, out[0].end_col),
			(Some(1), Some(15), Some(1), Some(35))
		);
		// Line 3 may open a later fragment, where its columns would not be off by
		// one: the report cannot tell, so only the row is kept.
		assert_eq!(
			(out[1].row, out[1].col, out[1].end_row, out[1].end_col),
			(Some(3), None, Some(3), None)
		);
	}

	#[test]
	fn a_secret_spanning_lines_corrects_each_end_by_its_own_line() {
		let json =
			br#"[{"Description":"d","RuleID":"private-key","StartLine":1,"StartColumn":1,"EndLine":4,"EndColumn":9}]"#;
		let d = &parse(json, b"", 0)[0];
		assert_eq!(
			(d.row, d.col, d.end_row, d.end_col),
			(Some(1), Some(1), Some(4), Some(9))
		);
	}

	#[test]
	fn never_sets_a_severity() {
		let json = br#"[{"Description":"d","RuleID":"r","StartLine":2,"level":"error"}]"#;
		assert_eq!(parse(json, b"", 0)[0].severity, None);
	}

	#[test]
	fn falls_back_to_stderr_when_stdout_empty() {
		assert_eq!(parse(b"", REPORT, 0).len(), 2);
	}

	#[test]
	fn empty_report_yields_nothing() {
		assert!(parse(b"", b"[]", 0).is_empty());
	}
	#[test]
	fn names_the_file_of_each_finding() {
		let json = br#"[{"RuleID":"r","Description":"d","StartLine":2,"EndLine":2,"StartColumn":5,"EndColumn":9,"File":"conf/app.env"}]"#;
		assert_eq!(parse(json, b"", 0)[0].file.as_deref(), Some("conf/app.env"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: tests::REPORT,
		stderr: b"7:25PM INF scanned ~63 bytes (63 bytes) in 81.6ms\n7:25PM WRN leaks found: 2\n",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: b"[]\n",
		stderr: b"7:25PM INF scanned ~8 bytes (8 bytes) in 1ms\n7:25PM INF no leaks found\n",
		exit: 0,
	},
];
