//! ansiblelint — Linter for Ansible playbooks, roles and collections.
//! Ported from the none-ls diagnostics/ansiblelint builtin.
//!
//! ansible-lint emits the Code Climate JSON array. Each issue is a nested object;
//! the location row/col live under either the new `location.lines.begin` shape or
//! the older `location.positions.begin.{line,column}` shape — mirroring the
//! builtin's `on_output`. We navigate both with `tinyjson` (the standard
//! `json_diag::from_json` flat mapper can't reach the nested fields).

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "ansiblelint",
    description: "Linter for Ansible playbooks, roles and collections.",
    url: "https://github.com/ansible-community/ansible-lint",
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
        severity: get_str(map, "severity").and_then(|s| severity_of(&s)),
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

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "blocker" | "critical" | "major" => Some(severity::ERROR),
        "minor" => Some(severity::WARNING),
        "info" => Some(severity::INFO),
        _ => None,
    }
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
                "description": "All names should start with an uppercase letter.",
                "severity": "minor",
                "location": {
                    "path": "playbook.yml",
                    "lines": { "begin": 12 }
                }
            }
        ]"#;
        let out = parse(json, b"", 2);
        assert_eq!(out.len(), 1);
        assert_eq!(
            out[0].message,
            "All names should start with an uppercase letter."
        );
        assert_eq!(out[0].code.as_deref(), Some("name[casing]"));
        assert_eq!(out[0].row, Some(12));
        assert_eq!(out[0].col, None);
        assert_eq!(out[0].severity, Some(severity::WARNING));
    }

    #[test]
    fn parses_positions_begin_shape_and_maps_major_to_error() {
        let json = br#"[
            {
                "check_name": "yaml[trailing-spaces]",
                "description": "Trailing spaces",
                "severity": "major",
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
    fn empty_array_and_invalid_yield_nothing() {
        assert!(parse(b"[]", b"", 0).is_empty());
        assert!(parse(b"not json", b"", 1).is_empty());
    }
}
