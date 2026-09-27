//! ansiblelint — Linter for Ansible playbooks, roles and collections.
//! Ported from the none-ls diagnostics/ansiblelint builtin.
//!
//! ansible-lint emits the Code Climate JSON array. Each issue is a nested object;
//! the location row/col live under either the new `location.lines.begin` shape or
//! the older `location.positions.begin.{line,column}` shape — mirroring the
//! builtin's `on_output`. We navigate both with `tinyjson` (the standard
//! `json_diag::from_json` flat mapper can't reach the nested fields).
//!
//! The level is ansible-lint's own `level` field (`error`, or `warning` for a
//! rule on the warn list). The Code Climate `severity` (`info` … `blocker`) is
//! the rule's impact rating, which says nothing about whether the finding fails
//! the run, so it is never read. `url` is the rule's documentation page.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "ansiblelint",
	description: "Linter for Ansible playbooks, roles and collections.",
	url: "https://github.com/ansible-community/ansible-lint",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		// to_temp_file=true: ansible-lint reads a real path, not stdin.
		args: &["-f", "codeclimate", "-q", "--nocolor", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let items = match &value {
		JsonValue::Array(items) => items,
		_ => return Vec::new(),
	};
	items.iter().filter_map(from_obj).collect()
}

fn from_obj(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = as_obj(value)?;
	// description is required (it becomes the message).
	let message = get_str(map, "description")?;
	let (row, col) = location_pos(map.get("location"));
	Some(RawDiagnostic {
		message,
		row,
		col,
		code: get_str(map, "check_name"),
		url: get_str(map, "url"),
		severity: get_str(map, "level").and_then(|s| severity::of(DESCRIPTOR.severities, &s)),
		..RawDiagnostic::default()
	})
}

/// Extract (row, col) from the `location` object, supporting both the newer
/// `lines.begin` shape (scalar line, or nested {line,column}) and the older
/// `positions.begin.{line,column}` shape.
fn location_pos(loc: Option<&JsonValue>) -> (Option<u32>, Option<u32>) {
	let map = match loc.and_then(as_obj) {
		Some(m) => m,
		None => return (None, None),
	};
	if let Some(lines) = map.get("lines").and_then(as_obj) {
		match lines.get("begin") {
			// New nested form: { line, column }.
			Some(JsonValue::Object(b)) => {
				return (get_u32_v(b.get("line")), get_u32_v(b.get("column")));
			}
			// Old scalar form: begin is the line number, no column.
			Some(v) => return (get_u32_v(Some(v)), None),
			None => return (None, None),
		}
	}
	if let Some(positions) = map.get("positions").and_then(as_obj) {
		if let Some(begin) = positions.get("begin").and_then(as_obj) {
			return (get_u32_v(begin.get("line")), get_u32_v(begin.get("column")));
		}
	}
	(None, None)
}

fn as_obj(value: &JsonValue) -> Option<&HashMap<String, JsonValue>> {
	match value {
		JsonValue::Object(m) => Some(m),
		_ => None,
	}
}

fn get_str(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn get_u32_v(v: Option<&JsonValue>) -> Option<u32> {
	match v {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_new_lines_begin_shape() {
		let json = br#"[
            {
                "type": "issue",
                "check_name": "name[casing]",
                "url": "https://ansible.readthedocs.io/projects/lint/rules/name/",
                "description": "All names should start with an uppercase letter.",
                "severity": "minor",
                "level": "warning",
                "location": {
                    "path": "playbook.yml",
                    "lines": { "begin": 12 }
                }
            }
        ]"#;
		let out = parse(json, b"", 2);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "All names should start with an uppercase letter.");
		assert_eq!(out[0].code.as_deref(), Some("name[casing]"));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://ansible.readthedocs.io/projects/lint/rules/name/")
		);
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn parses_positions_begin_shape_and_reads_the_level() {
		let json = br#"[
            {
                "check_name": "yaml[trailing-spaces]",
                "description": "Trailing spaces",
                "severity": "minor",
                "level": "error",
                "location": {
                    "path": "roles/x/tasks/main.yml",
                    "positions": { "begin": { "line": 4, "column": 9 } }
                }
            }
        ]"#;
		let out = parse(json, b"", 2);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(4));
		assert_eq!(out[0].col, Some(9));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].code.as_deref(), Some("yaml[trailing-spaces]"));
	}

	#[test]
	fn the_impact_severity_is_never_a_level() {
		let json = br#"[
            {"check_name": "a", "description": "no level", "severity": "blocker", "location": {"lines": {"begin": 1}}},
            {"check_name": "b", "description": "odd level", "level": "fatal", "location": {"lines": {"begin": 2}}}
        ]"#;
		let out = parse(json, b"", 2);
		assert_eq!(out.len(), 2);
		assert!(out.iter().all(|d| d.severity.is_none()));
	}

	#[test]
	fn empty_array_and_invalid_yield_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
		assert!(parse(b"not json", b"", 1).is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"[{"type":"issue","check_name":"name[casing]","categories":["idiom"],"url":"https://ansible.readthedocs.io/projects/lint/rules/name/","severity":"minor","level":"warning","description":"All names should start with an uppercase letter.","fingerprint":"1f0e","location":{"path":"playbook.yml","lines":{"begin":3}}},{"type":"issue","check_name":"yaml[trailing-spaces]","categories":["formatting","yaml"],"url":"https://ansible.readthedocs.io/projects/lint/rules/yaml/","severity":"minor","level":"error","description":"Trailing spaces","fingerprint":"9ab2","location":{"path":"playbook.yml","positions":{"begin":{"line":7,"column":21}}}}]"#,
	stderr: b"",
	exit: 2,
}];
