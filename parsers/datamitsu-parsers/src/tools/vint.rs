//! vint — Linter for Vimscript. Ported from the none-ls diagnostics/vint builtin.
//!
//! `--json` prints an array of violations whose `severity` is the lowercased
//! level name: `error`, `warning` or `style_problem`.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "vint",
	description: "Linter for Vimscript.",
	url: "https://github.com/Vimjas/vint",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("style_problem", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
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
		file: "file_path",
		..Attrs::defaults()
	};
	json_diag::from_json(stdout, &attrs, severity_of)
}

fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
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
	fn reads_error_and_warning() {
		let s = &SAMPLES[0];
		let levels: Vec<_> = parse(s.stdout, s.stderr, s.exit).iter().map(|d| d.severity).collect();
		assert_eq!(
			levels,
			[Some(severity::ERROR), Some(severity::WARNING), Some(severity::INFO)]
		);
	}

	#[test]
	fn unknown_severity_is_none() {
		let json = br#"[{"line_number":1,"column_number":1,"severity":"fatal","description":"x"}]"#;
		let out = parse(json, b"", 1);
		assert_eq!(out[0].severity, None);
	}
	#[test]
	fn names_the_file_of_each_problem() {
		let json = br#"[{"file_path":"plugin/a.vim","line_number":1,"column_number":1,"policy_name":"P","description":"d","severity":"warning"}]"#;
		assert_eq!(parse(json, b"", 1)[0].file.as_deref(), Some("plugin/a.vim"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"[{"file_path": "plugin/foo.vim", "line_number": 3, "column_number": 1, "severity": "error", "description": "E488: Trailing characters", "policy_name": "SyntaxError", "reference": ":help E488"}, {"file_path": "plugin/foo.vim", "line_number": 7, "column_number": 5, "severity": "warning", "description": "Use the full option name instead of the abbreviation (see Anti-pattern of vimrc (Plugin layout))", "policy_name": "ProhibitAbbreviationOption", "reference": ":help option-summary"}, {"file_path": "plugin/foo.vim", "line_number": 12, "column_number": 5, "severity": "style_problem", "description": "Make the scope explicit like `l:count` (see Anti-pattern of vimrc (Scope of identifier))", "policy_name": "ProhibitImplicitScopeVariable", "reference": "Anti-pattern of vimrc (Scope of identifier)"}]"#,
	stderr: b"",
	exit: 1,
}];
