//! golangci_lint — A Go linter aggregator. Ported from the none-ls diagnostics/golangci_lint builtin.
//!
//! golangci-lint's JSON output is a single object:
//! `{"Issues":[{"FromLinter":"...","Text":"...","Severity":"","Pos":{"Filename":"...","Line":3,"Column":7}}],"Report":{...}}`.
//! none-ls navigates to `output.Issues` and, for each issue, reads `Pos.Line` /
//! `Pos.Column` (a nested object) and `Text` as the message. The linter that
//! reported an issue (`FromLinter`) is its code; a linter's own rule id, when it
//! prints one, stays in the message. `Severity` is the level a project's
//! `severity` rules assign, and is empty when none applies.
//!
//! When `Report.Error` is present the builtin logs the error and returns no
//! diagnostics — we mirror that by skipping issue extraction in that case.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "golangci_lint",
	description: "A Go linter aggregator.",
	url: "https://golangci-lint.run/",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("info", severity::INFO),
	],
	column_unit: "utf-8",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		// none-ls feeds buffer content on stdin (to_stdin=true) and runs the v2
		// JSON path-to-stdout form. golangci-lint reads from stdin itself; there is
		// no file/stdin argument placeholder.
		args: &[
			"run",
			"--fix=false",
			"--show-stats=false",
			"--output.json.path=stdout",
			"--path-mode=abs",
		],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// Lenient: one golangci-lint run covers a whole module, and the human summary
	// the tool itself prints after the JSON would otherwise discard every issue.
	crate::tools::json_diag::extract_lenient(stdout, from_report)
}

fn from_report(value: &JsonValue) -> Vec<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return Vec::new(),
	};

	// Report.Error present -> none-ls logs it and returns nothing.
	if let Some(JsonValue::Object(report)) = map.get("Report") {
		if matches!(report.get("Error"), Some(JsonValue::String(_))) {
			return Vec::new();
		}
	}

	let issues = match map.get("Issues") {
		Some(JsonValue::Array(items)) => items,
		_ => return Vec::new(),
	};

	issues.iter().filter_map(issue_to_diag).collect()
}

fn issue_to_diag(issue: &JsonValue) -> Option<RawDiagnostic> {
	let map = match issue {
		JsonValue::Object(m) => m,
		_ => return None,
	};

	let message = match map.get("Text") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};

	// One run reports issues across the whole module, so Pos.Filename is what
	// attributes each of them.
	let (row, col, file) = match map.get("Pos") {
		Some(JsonValue::Object(pos)) => (
			get_u32(pos, "Line"),
			get_u32(pos, "Column"),
			match pos.get("Filename") {
				Some(JsonValue::String(s)) => crate::diagnostic::exact_file_field(s),
				_ => None,
			},
		),
		_ => (None, None, None),
	};

	Some(RawDiagnostic {
		message,
		row,
		col,
		severity: match map.get("Severity") {
			Some(JsonValue::String(s)) => severity::of(DESCRIPTOR.severities, s),
			_ => None,
		},
		source: Some("golangci-lint".to_string()),
		code: match map.get("FromLinter") {
			Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
			_ => None,
		},
		file,
		..RawDiagnostic::default()
	})
}

/// A 1-based position; go/token's `0` means the linter reported none.
fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n).filter(|&n| n > 0),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const ISSUES: &[u8] = br#"{"Issues":[
            {"FromLinter":"errcheck","Text":"Error return value is not checked","Severity":"","Pos":{"Filename":"main.go","Line":12,"Column":5}},
            {"FromLinter":"govet","Text":"unreachable code","Severity":"","Pos":{"Filename":"main.go","Line":20,"Column":1}}
        ],"Report":{"Linters":[]}}
2 issues:
* errcheck: 1
* govet: 1
"#;

	pub(super) const WITH_SEVERITY: &[u8] = br#"{"Issues":[
            {"FromLinter":"errcheck","Text":"Error return value is not checked","Severity":"error","Pos":{"Filename":"main.go","Line":12,"Column":5}},
            {"FromLinter":"revive","Text":"exported: exported function Run should have comment or be unexported","Severity":"warning","Pos":{"Filename":"run.go","Line":3,"Column":1}},
            {"FromLinter":"godot","Text":"Comment should end in a period","Severity":"info","Pos":{"Filename":"run.go","Line":2,"Column":30}},
            {"FromLinter":"lll","Text":"The line is 130 characters long, which exceeds the maximum of 120 characters.","Severity":"low","Pos":{"Filename":"run.go","Line":9,"Column":0}}
        ],"Report":{}}"#;

	#[test]
	fn parses_issues_with_nested_pos() {
		let out = parse(ISSUES, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Error return value is not checked");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].source.as_deref(), Some("golangci-lint"));
		assert_eq!(out[0].code.as_deref(), Some("errcheck"));
		assert_eq!(out[1].source.as_deref(), Some("golangci-lint"));
		assert_eq!(out[1].code.as_deref(), Some("govet"));
		assert_eq!(out[1].row, Some(20));
	}

	#[test]
	fn an_issue_without_a_severity_has_no_level() {
		// golangci-lint prints "" when no severity rule applies, and older
		// releases omit the field.
		let out = parse(ISSUES, b"", 1);
		assert!(out.iter().all(|d| d.severity.is_none()));
		let missing = br#"{"Issues":[{"FromLinter":"errcheck","Text":"t","Pos":{"Line":1,"Column":1}}]}"#;
		assert_eq!(parse(missing, b"", 1)[0].severity, None);
	}

	#[test]
	fn reads_the_severity_a_project_assigns() {
		let out = parse(WITH_SEVERITY, b"", 1);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[2].severity, Some(severity::INFO));
		// A severity name the project made up has no level on the shared scale.
		assert_eq!(out[3].severity, None);
	}

	#[test]
	fn keeps_a_linters_own_rule_id_in_the_message() {
		let out = parse(WITH_SEVERITY, b"", 1);
		assert_eq!(out[1].code.as_deref(), Some("revive"));
		assert!(out[1].message.starts_with("exported: "));
	}

	#[test]
	fn a_zero_column_is_no_column() {
		let out = parse(WITH_SEVERITY, b"", 1);
		assert_eq!((out[3].row, out[3].col), (Some(9), None));
	}

	#[test]
	fn report_error_yields_nothing() {
		let json =
			br#"{"Issues":[{"FromLinter":"x","Text":"y","Pos":{"Line":1,"Column":1}}],"Report":{"Error":"config failed"}}"#;
		assert!(parse(json, b"", 1).is_empty());
	}

	#[test]
	fn null_issues_yield_nothing() {
		assert!(parse(br#"{"Issues":null,"Report":{}}"#, b"", 0).is_empty());
	}
	#[test]
	fn reports_the_path_per_issue() {
		let json = br#"{"Issues":[
            {"FromLinter":"errcheck","Text":"unchecked error","Pos":{"Filename":"cmd/root.go","Line":12,"Column":5}},
            {"FromLinter":"govet","Text":"unreachable","Pos":{"Filename":"internal/x/y.go","Line":20,"Column":1}}
        ]}"#;
		let out = parse(json, b"", 1);
		assert_eq!(out[0].file.as_deref(), Some("cmd/root.go"));
		assert_eq!(out[1].file.as_deref(), Some("internal/x/y.go"));
	}

	#[test]
	fn skips_noise_printed_before_the_json() {
		let noisy = br#"go: downloading example.com/mod v1.2.3
{"Issues":[{"FromLinter":"errcheck","Text":"unchecked error","Pos":{"Filename":"main.go","Line":1,"Column":1}}]}"#;
		assert_eq!(parse(noisy, b"", 1).len(), 1);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: tests::ISSUES,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: tests::WITH_SEVERITY,
		stderr: b"",
		exit: 1,
	},
];
