//! golangci_lint — A Go linter aggregator. Ported from the none-ls diagnostics/golangci_lint builtin.
//!
//! golangci-lint's JSON output is a single object:
//! `{"Issues":[{"FromLinter":"...","Text":"...","Pos":{"Filename":"...","Line":3,"Column":7}}],"Report":{...}}`.
//! none-ls navigates to `output.Issues` and, for each issue, reads `Pos.Line` /
//! `Pos.Column` (a nested object), `Text` as the message, and `FromLinter` for the
//! source (`golangci-lint: <FromLinter>`). Severity is hard-coded to WARNING for
//! every issue; the tool emits no level token or rule id, so `code` stays None.
//!
//! When `Report.Error` is present the builtin logs the error and returns no
//! diagnostics — we mirror that by skipping issue extraction in that case.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "golangci_lint",
    description: "A Go linter aggregator.",
    url: "https://golangci-lint.run/",
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
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };

    let map = match &value {
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

    let (row, col) = match map.get("Pos") {
        Some(JsonValue::Object(pos)) => (get_u32(pos, "Line"), get_u32(pos, "Column")),
        _ => (None, None),
    };

    let source = match map.get("FromLinter") {
        Some(JsonValue::String(s)) => Some(format!("golangci-lint: {s}")),
        _ => None,
    };

    Some(RawDiagnostic {
        message,
        row,
        col,
        severity: Some(severity::WARNING),
        source,
        ..RawDiagnostic::default()
    })
}

fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
    match map.get(key) {
        Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_issues_with_nested_pos() {
        let json = br#"{"Issues":[
            {"FromLinter":"errcheck","Text":"Error return value is not checked","Pos":{"Filename":"main.go","Line":12,"Column":5}},
            {"FromLinter":"govet","Text":"unreachable code","Pos":{"Filename":"main.go","Line":20,"Column":1}}
        ],"Report":{"Linters":[]}}"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Error return value is not checked");
        assert_eq!(out[0].row, Some(12));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].source.as_deref(), Some("golangci-lint: errcheck"));
        assert_eq!(out[0].code, None);
        assert_eq!(out[1].source.as_deref(), Some("golangci-lint: govet"));
        assert_eq!(out[1].row, Some(20));
    }

    #[test]
    fn report_error_yields_nothing() {
        let json = br#"{"Issues":[{"FromLinter":"x","Text":"y","Pos":{"Line":1,"Column":1}}],"Report":{"Error":"config failed"}}"#;
        assert!(parse(json, b"", 1).is_empty());
    }

    #[test]
    fn null_issues_yield_nothing() {
        assert!(parse(br#"{"Issues":null,"Report":{}}"#, b"", 0).is_empty());
    }
}
