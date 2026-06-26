//! opacheck — check Rego source files for parse and compilation errors.
//! Ported from the none-ls diagnostics/opacheck builtin.
//!
//! OPA's `check -f json` emits `{"errors":[{message, code, location:{file,row,col}}]}`.
//! The builtin keeps only entries that carry a `location` (others are global,
//! non-diagnostic errors) and pins every diagnostic to ERROR severity.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "opacheck",
    description: "Check Rego source files for parse and compilation errors.",
    url: "https://www.openpolicyagent.org/docs/latest/cli/#opa-check",
    operations: &[Operation {
        mode: "lint",
        args: &[
            "check",
            "-f",
            "json",
            "--strict",
            "{file}",
            "--ignore=*.yaml",
            "--ignore=*.yml",
            "--ignore=*.json",
            "--ignore=.git/**/*",
        ],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // from_stderr = true: OPA reports check failures on stderr.
    let mut out = parse_bytes(stderr);
    if out.is_empty() {
        out = parse_bytes(stdout);
    }
    out
}

fn parse_bytes(bytes: &[u8]) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(bytes);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let errors = match &value {
        JsonValue::Object(m) => match m.get("errors") {
            Some(JsonValue::Array(items)) => items,
            _ => return Vec::new(),
        },
        _ => return Vec::new(),
    };
    errors.iter().filter_map(diag_from_error).collect()
}

fn diag_from_error(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = match value {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    // Only entries with a location are surfaced as diagnostics.
    let location = match map.get("location") {
        Some(JsonValue::Object(loc)) => loc,
        _ => return None,
    };
    let message = match map.get("message") {
        Some(JsonValue::String(s)) => s.clone(),
        _ => return None,
    };
    let row = location.get("row").and_then(as_u32);
    let col = location.get("col").and_then(as_u32);
    let code = match map.get("code") {
        Some(JsonValue::String(s)) => Some(s.clone()),
        _ => None,
    };
    Some(RawDiagnostic {
        message,
        row,
        col,
        severity: Some(severity::ERROR),
        source: Some("opacheck".to_string()),
        code,
        ..RawDiagnostic::default()
    })
}

fn as_u32(v: &JsonValue) -> Option<u32> {
    match v {
        JsonValue::Number(n) => crate::numconv::json_u32(*n),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_with_location() {
        let json = br#"{"errors":[{"message":"rego_parse_error: unexpected eof","code":"rego_parse_error","location":{"file":"policy.rego","row":4,"col":1}}]}"#;
        let out = parse(&[], json, 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "rego_parse_error: unexpected eof");
        assert_eq!(out[0].row, Some(4));
        assert_eq!(out[0].col, Some(1));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[0].code.as_deref(), Some("rego_parse_error"));
        assert_eq!(out[0].source.as_deref(), Some("opacheck"));
    }

    #[test]
    fn skips_errors_without_location() {
        let json = br#"{"errors":[{"message":"loading error: no files found"}]}"#;
        let out = parse(&[], json, 1);
        assert!(out.is_empty());
    }

    #[test]
    fn empty_or_missing_errors_yields_nothing() {
        assert!(parse(&[], br#"{"errors":null}"#, 0).is_empty());
        assert!(parse(&[], b"not json", 1).is_empty());
    }
}
