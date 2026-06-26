//! actionlint — static checker for GitHub Actions workflow files.
//! Ported from the none-ls diagnostics/actionlint builtin.
//!
//! actionlint emits a JSON array via `-format '{{json .}}'`. Each object carries
//! `message`, `line`, `column`, and `kind` (mapped to `code`). none-ls pins
//! `source = "actionlint"` and `severity = 1` (ERROR) as constants — the tool
//! itself emits no level token — so we set both after the field mapping.
//! Diagnostics are written to stderr (`from_stderr = true`).

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "actionlint",
	description: "Actionlint is a static checker for GitHub Actions workflow files.",
	url: "https://github.com/rhysd/actionlint",
	operations: &[Operation {
		mode: "lint",
		args: &["-no-color", "-format", "{{json .}}", "-"],
		stdin: true,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// actionlint writes its JSON report to stderr (from_stderr = true).
	let attrs = Attrs {
		code: "kind",
		..Attrs::defaults()
	};
	let mut out = json_diag::from_json(stderr, &attrs, severity_of);
	for d in &mut out {
		// none-ls pins these as constants; the tool emits no level token.
		d.severity = Some(severity::ERROR);
		d.source = Some("actionlint".to_string());
	}
	out
}

/// actionlint emits no severity token; the builtin hardcodes ERROR instead.
fn severity_of(_level: &str) -> Option<u8> {
	None
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_actionlint_json() {
		let json = br#"[{"message":"shellcheck reported issue in this script","filepath":".github/workflows/ci.yaml","line":21,"column":9,"kind":"shellcheck","snippet":"echo hi"},{"message":"property \"foo\" is not defined","filepath":".github/workflows/ci.yaml","line":3,"column":5,"kind":"expression"}]"#;
		let out = parse(b"", json, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "shellcheck reported issue in this script");
		assert_eq!(out[0].row, Some(21));
		assert_eq!(out[0].col, Some(9));
		assert_eq!(out[0].code.as_deref(), Some("shellcheck"));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("actionlint"));
		assert_eq!(out[1].code.as_deref(), Some("expression"));
		assert_eq!(out[1].severity, Some(severity::ERROR));
	}

	#[test]
	fn empty_output_yields_nothing() {
		assert!(parse(b"", b"", 0).is_empty());
	}
}
