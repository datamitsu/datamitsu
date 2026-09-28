//! Code Climate issues, the shape of GitLab Code Quality reports: a JSON array
//! whose every element is an object with `check_name` and `location.path`.
//!
//! The rule is `check_name`, the message `description`; the position is
//! `location.positions` when printed, else `location.lines`, passed through.
//! `fingerprint` is ignored: the core computes its own.
use tinyjson::JsonValue;

use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::json_diag::{at, elements, member, position, text};
use crate::response::Response;
use crate::severity::{self, Level};

const LEVELS: &[Level] = &[
	Level("blocker", severity::ERROR),
	Level("critical", severity::ERROR),
	Level("major", severity::ERROR),
	Level("minor", severity::WARNING),
	Level("info", severity::INFO),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "codeclimate",
	description: "Code Climate issues, as GitLab Code Quality reads them: `ruff check --output-format gitlab`, \
        `golangci-lint run --output.code-climate.path=stdout`. Recognized by a JSON array whose every \
        element carries `check_name` and `location.path`.",
	url: "https://github.com/codeclimate/platform/blob/master/spec/analyzers/SPEC.md",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	for stream in [stdout, stderr] {
		if let Some(diags) = crate::json_diag::find_envelope(stream, from_report) {
			return Response::recognized(DESCRIPTOR.name, diags);
		}
	}
	Response::unrecognized(DESCRIPTOR.name)
}

fn from_report(report: &JsonValue) -> Option<Vec<RawDiagnostic>> {
	let issues = elements(report).filter(|a| !a.is_empty())?;
	if !issues.iter().all(is_issue) {
		return None;
	}
	Some(issues.iter().filter_map(issue).collect())
}

fn is_issue(v: &JsonValue) -> bool {
	member(v, "check_name").and_then(text).is_some() && at(v, &["location", "path"]).and_then(text).is_some()
}

fn issue(v: &JsonValue) -> Option<RawDiagnostic> {
	let message = member(v, "description").and_then(text)?;
	let location = member(v, "location");
	let num = |path: &[&str]| location.and_then(|l| at(l, path)).and_then(position);
	let (row, col, end_row, end_col) = match location.and_then(|l| member(l, "positions")) {
		Some(_) => (
			num(&["positions", "begin", "line"]),
			num(&["positions", "begin", "column"]),
			num(&["positions", "end", "line"]),
			num(&["positions", "end", "column"]),
		),
		None => (num(&["lines", "begin"]), None, num(&["lines", "end"]), None),
	};
	Some(RawDiagnostic {
		message: message.to_string(),
		row,
		col,
		end_row,
		end_col,
		severity: member(v, "severity")
			.and_then(text)
			.and_then(|s| severity::of(LEVELS, s)),
		code: member(v, "check_name").and_then(text).map(str::to_string),
		file: at(v, &["location", "path"])
			.and_then(text)
			.and_then(crate::diagnostic::file_field),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const REPORT: &[u8] = br#"[
  {
    "check_name": "F401",
    "description": "`os` imported but unused",
    "fingerprint": "4b6f",
    "location": { "lines": { "begin": 1, "end": 1 }, "path": "src/a.py" },
    "severity": "major"
  },
  {
    "check_name": "errcheck",
    "description": "Error return value is not checked",
    "location": { "path": "pkg/b.go", "positions": { "begin": { "column": 5, "line": 12 }, "end": { "column": 9, "line": 12 } } },
    "severity": "minor"
  }
]"#;

	#[test]
	fn reads_lines_and_positions() {
		let r = parse(REPORT, b"", 1);
		assert!(r.recognized);
		let (a, b) = (&r.diagnostics[0], &r.diagnostics[1]);
		assert_eq!(
			(a.file.as_deref(), a.row, a.col, a.end_row),
			(Some("src/a.py"), Some(1), None, Some(1))
		);
		assert_eq!((a.code.as_deref(), a.severity), (Some("F401"), Some(severity::ERROR)));
		assert_eq!(
			(b.row, b.col, b.end_row, b.end_col),
			(Some(12), Some(5), Some(12), Some(9))
		);
		assert_eq!(b.severity, Some(severity::WARNING));
	}

	#[test]
	fn an_array_of_something_else_is_no_report() {
		for out in [
			&b"[]"[..],
			br#"[{"check_name":"x"}]"#,
			br#"[{"check_name":"x","location":{"path":"a"},"description":"d"},{"message":"other"}]"#,
			br#"{"check_name":"x","location":{"path":"a"}}"#,
			br#"[{"check_name":"x","description":"cut","location":{"path":"a"}}"#,
		] {
			assert!(!parse(out, b"", 1).recognized, "{}", String::from_utf8_lossy(out));
		}
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: tests::REPORT,
	stderr: b"",
	exit: 1,
}];
