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

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "gitleaks",
    description: "Gitleaks is a SAST tool for detecting and preventing hardcoded secrets like passwords, API keys, and tokens in git repos.",
    url: "https://github.com/gitleaks/gitleaks",
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
		..Attrs::defaults()
	};
	let mut diags = json_diag::from_json(bytes, &attrs, severity_of);
	for d in &mut diags {
		d.source = Some("gitleaks".to_string());
	}
	diags
}

/// gitleaks findings carry no severity token; the core supplies its fallback.
fn severity_of(_level: &str) -> Option<u8> {
	None
}

#[cfg(test)]
mod tests {
	use super::*;

	const SAMPLE: &[u8] = br#"[
          {
            "Description": "AWS Access Key",
            "RuleID": "aws-access-token",
            "StartLine": 12,
            "StartColumn": 5,
            "EndLine": 12,
            "EndColumn": 25,
            "Secret": "REDACTED-FAKE-TEST-SECRET"
          }
        ]"#;

	#[test]
	fn parses_findings_from_stdout() {
		// `--report-path -` writes the findings to stdout (stderr carries logs).
		let out = parse(SAMPLE, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "AWS Access Key");
		assert_eq!(out[0].code.as_deref(), Some("aws-access-token"));
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_row, Some(12));
		assert_eq!(out[0].end_col, Some(25));
		assert_eq!(out[0].source.as_deref(), Some("gitleaks"));
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn falls_back_to_stderr_when_stdout_empty() {
		assert_eq!(parse(b"", SAMPLE, 0).len(), 1);
	}

	#[test]
	fn empty_report_yields_nothing() {
		assert!(parse(b"", b"[]", 0).is_empty());
	}
}
