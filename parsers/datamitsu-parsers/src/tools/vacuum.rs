//! vacuum — OpenAPI linter. Ported from the none-ls diagnostics/vacuum builtin.
//!
//! vacuum emits JSON shaped as `{ "resultSet": { "results": [ … ] } }`, where each
//! result is `{ message, ruleId, ruleSeverity, range: { start:{line,character},
//! end:{line,character} } }`. That nesting is beyond the flat `json_diag::Attrs`
//! mapper, so the navigation is hand-written over `tinyjson`. Coordinates are
//! passed through exactly as the builtin does (vacuum's LSP-style 0-based
//! line/character — the Go core owns any rebasing).

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "vacuum",
    description: "The world\u{2019}s fastest and most scalable OpenAPI linter.",
    url: "https://quobix.com/vacuum",
    operations: &[Operation {
        mode: "lint",
        args: &["report", "--stdin", "--stdout"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let results = match value
        .get::<HashMap<String, JsonValue>>()
        .and_then(|m| m.get("resultSet"))
        .and_then(|rs| rs.get::<HashMap<String, JsonValue>>())
        .and_then(|m| m.get("results"))
        .and_then(|r| r.get::<Vec<JsonValue>>())
    {
        Some(r) => r,
        None => return Vec::new(),
    };
    results.iter().filter_map(diag_of).collect()
}

fn diag_of(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = value.get::<HashMap<String, JsonValue>>()?;
    let message = get_str(map, "message")?;
    let (row, col) = range_point(map, "start");
    let (end_row, end_col) = range_point(map, "end");
    Some(RawDiagnostic {
        message,
        row,
        col,
        end_row,
        end_col,
        source: Some("Vacuum".to_string()),
        code: get_str(map, "ruleId"),
        severity: get_str(map, "ruleSeverity").and_then(|s| severity_of(&s)),
        ..RawDiagnostic::default()
    })
}

/// Reads `range.<which>.line` / `range.<which>.character`.
fn range_point(map: &HashMap<String, JsonValue>, which: &str) -> (Option<u32>, Option<u32>) {
    let point = map
        .get("range")
        .and_then(|r| r.get::<HashMap<String, JsonValue>>())
        .and_then(|m| m.get(which))
        .and_then(|p| p.get::<HashMap<String, JsonValue>>());
    match point {
        Some(p) => (get_u32(p, "line"), get_u32(p, "character")),
        None => (None, None),
    }
}

fn get_str(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
    map.get(key).and_then(|v| v.get::<String>()).cloned()
}

fn get_u32(map: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
    match map.get(key) {
        Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
        _ => None,
    }
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" => Some(severity::ERROR),
        "warn" => Some(severity::WARNING),
        "info" => Some(severity::INFO),
        "hint" => Some(severity::HINT),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_nested_result() {
        let json = br#"{
            "resultSet": {
                "results": [
                    {
                        "message": "Operation must define an operationId",
                        "ruleId": "operation-operationId",
                        "ruleSeverity": "warn",
                        "range": {
                            "start": { "line": 12, "character": 4 },
                            "end": { "line": 12, "character": 20 }
                        }
                    }
                ]
            }
        }"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "Operation must define an operationId");
        assert_eq!(out[0].code.as_deref(), Some("operation-operationId"));
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].source.as_deref(), Some("Vacuum"));
        assert_eq!(out[0].row, Some(12));
        assert_eq!(out[0].col, Some(4));
        assert_eq!(out[0].end_row, Some(12));
        assert_eq!(out[0].end_col, Some(20));
    }

    #[test]
    fn no_results_yields_empty() {
        assert!(parse(br#"{"resultSet":{"results":[]}}"#, b"", 0).is_empty());
        assert!(parse(br#"{"resultSet":{}}"#, b"", 0).is_empty());
    }

    #[test]
    fn unknown_severity_left_unset() {
        let json = br#"{"resultSet":{"results":[
            {"message":"x","ruleId":"r","ruleSeverity":"bogus",
             "range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}
        ]}}"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].severity, None);
    }
}
