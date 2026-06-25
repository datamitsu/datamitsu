//! revive — Fast, configurable, extensible, flexible, and beautiful linter for Go.
//! Ported from the none-ls diagnostics/revive builtin.
//!
//! revive's JSON output is a top-level array of failures:
//! `[{"Severity":"warning","Failure":"...","Position":{"Start":{"Line":3,"Column":7},"End":{"Line":3,"Column":12}}}]`.
//! none-ls reads `Failure` as the message, `Severity` (error/warning) for level,
//! and the nested `Position.Start` / `Position.End` for the span. The source is
//! hard-coded to "revive". The tool emits no rule id, so `code` stays None.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "revive",
    description: "Fast, configurable, extensible, flexible, and beautiful linter for Go.",
    url: "https://revive.run/",
    // to_stdin=false and the builtin lints the whole package (`./...`); there is no
    // per-file/stdin placeholder. revive does not accept extra non-file args after
    // the target, so this is the full canonical invocation.
    operations: &[Operation {
        mode: "lint",
        args: &["-formatter", "json", "./..."],
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

    items.iter().filter_map(failure_to_diag).collect()
}

fn failure_to_diag(item: &JsonValue) -> Option<RawDiagnostic> {
    let map = match item {
        JsonValue::Object(m) => m,
        _ => return None,
    };

    let message = match map.get("Failure") {
        Some(JsonValue::String(s)) => s.clone(),
        _ => return None,
    };

    let (row, col, end_row, end_col) = match map.get("Position") {
        Some(JsonValue::Object(pos)) => {
            let (row, col) = match pos.get("Start") {
                Some(JsonValue::Object(s)) => (get_u32(s, "Line"), get_u32(s, "Column")),
                _ => (None, None),
            };
            let (end_row, end_col) = match pos.get("End") {
                Some(JsonValue::Object(e)) => (get_u32(e, "Line"), get_u32(e, "Column")),
                _ => (None, None),
            };
            (row, col, end_row, end_col)
        }
        _ => (None, None, None, None),
    };

    let severity = match map.get("Severity") {
        Some(JsonValue::String(s)) => severity_of(s),
        _ => None,
    };

    Some(RawDiagnostic {
        message,
        row,
        col,
        end_row,
        end_col,
        severity,
        source: Some("revive".to_string()),
        ..RawDiagnostic::default()
    })
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        _ => None,
    }
}

fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
    match map.get(key) {
        Some(JsonValue::Number(n)) if n.is_finite() && *n >= 0.0 => Some(*n as u32),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_failures_with_nested_position() {
        let json = br#"[
            {"Severity":"warning","Failure":"exported function Foo should have comment","Position":{"Start":{"Filename":"main.go","Line":12,"Column":1},"End":{"Filename":"main.go","Line":12,"Column":20}}},
            {"Severity":"error","Failure":"redefinition of the built-in function len","Position":{"Start":{"Filename":"main.go","Line":3,"Column":5},"End":{"Filename":"main.go","Line":3,"Column":8}}}
        ]"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "exported function Foo should have comment");
        assert_eq!(out[0].row, Some(12));
        assert_eq!(out[0].col, Some(1));
        assert_eq!(out[0].end_row, Some(12));
        assert_eq!(out[0].end_col, Some(20));
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].source.as_deref(), Some("revive"));
        assert_eq!(out[0].code, None);
        assert_eq!(out[1].severity, Some(severity::ERROR));
        assert_eq!(out[1].row, Some(3));
    }

    #[test]
    fn empty_array_yields_nothing() {
        assert!(parse(b"[]", b"", 0).is_empty());
    }

    #[test]
    fn invalid_json_yields_nothing() {
        assert!(parse(b"not json", b"", 1).is_empty());
    }
}
