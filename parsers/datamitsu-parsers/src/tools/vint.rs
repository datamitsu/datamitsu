//! vint — Linter for Vimscript. Ported from the none-ls diagnostics/vint builtin.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "vint",
	description: "Linter for Vimscript.",
	url: "https://github.com/Vimjas/vint",
	operations: &[Operation {
		mode: "lint",
		args: &["--style-problem", "--json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let attrs = Attrs {
		row: "line_number",
		col: "column_number",
		code: "policy_name",
		message: "description",
		severity: "severity",
		..Attrs::defaults()
	};
	json_diag::from_json(stdout, &attrs, severity_of)
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"style_problem" => Some(severity::INFO),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_style_problem() {
		let json = br#"[{"line_number":12,"column_number":5,"policy_name":"ProhibitImplicitScopeVariable","severity":"style_problem","description":"Make the scope explicit"}]"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Make the scope explicit");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].code.as_deref(), Some("ProhibitImplicitScopeVariable"));
		assert_eq!(out[0].severity, Some(severity::INFO));
	}

	#[test]
	fn unknown_severity_is_none() {
		let json = br#"[{"line_number":1,"column_number":1,"severity":"warning","description":"x"}]"#;
		let out = parse(json, b"", 1);
		assert_eq!(out[0].severity, None);
	}
}
