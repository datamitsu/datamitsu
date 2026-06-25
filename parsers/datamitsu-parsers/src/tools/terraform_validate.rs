//! terraform_validate — Terraform validate subcommand validates configuration
//! files in a directory. Ported from the none-ls diagnostics/terraform_validate
//! builtin.
//!
//! `terraform validate -json` emits a JSON object on stderr of the shape:
//! `{ "diagnostics": [ { "severity", "summary", "detail",
//!   "range": { "filename", "start": {"line","column"}, "end": {"line","column"} } } ] }`.
//! The message is `summary`, with ` - <detail>` appended when `detail` is present.
//! Row/col come from `range.start`, end_row/end_col from `range.end`.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "terraform_validate",
    description: "Terraform validate is is a subcommand of terraform to validate configuration files in a directory",
    url: "https://github.com/hashicorp/terraform",
    operations: &[Operation {
        mode: "lint",
        args: &["validate", "-json"],
        stdin: false,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // from_stderr = true: terraform emits the JSON report on stderr.
    let text = String::from_utf8_lossy(stderr);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let map = match &value {
        JsonValue::Object(m) => m,
        _ => return Vec::new(),
    };
    let diags = match map.get("diagnostics") {
        Some(JsonValue::Array(items)) => items,
        _ => return Vec::new(),
    };
    diags.iter().filter_map(from_diag).collect()
}

fn from_diag(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = match value {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    let summary = get_str(map, "summary")?;
    let message = match get_str(map, "detail") {
        Some(detail) => format!("{summary} - {detail}"),
        None => summary,
    };
    let severity = get_str(map, "severity")
        .as_deref()
        .and_then(severity_of);

    let mut diag = RawDiagnostic {
        message,
        source: Some("terraform validate".to_string()),
        severity,
        ..RawDiagnostic::default()
    };

    if let Some(JsonValue::Object(range)) = map.get("range") {
        if let Some(JsonValue::Object(start)) = range.get("start") {
            diag.row = get_u32(start, "line");
            diag.col = get_u32(start, "column");
        }
        if let Some(JsonValue::Object(end)) = range.get("end") {
            diag.end_row = get_u32(end, "line");
            diag.end_col = get_u32(end, "column");
        }
    }

    Some(diag)
}

/// none-ls `h.diagnostics.severities` mapping.
fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        "information" => Some(severity::INFO),
        "hint" => Some(severity::HINT),
        _ => None,
    }
}

fn get_str(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<String> {
    match map.get(key) {
        Some(JsonValue::String(s)) => Some(s.clone()),
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
    fn parses_error_with_range_and_detail() {
        let stderr = br#"{
            "format_version": "1.0",
            "valid": false,
            "error_count": 1,
            "diagnostics": [
                {
                    "severity": "error",
                    "summary": "Unsupported argument",
                    "detail": "An argument named \"foo\" is not expected here.",
                    "range": {
                        "filename": "main.tf",
                        "start": { "line": 3, "column": 5, "byte": 40 },
                        "end": { "line": 3, "column": 8, "byte": 43 }
                    }
                }
            ]
        }"#;
        let out = parse(b"", stderr, 1);
        assert_eq!(out.len(), 1);
        assert_eq!(
            out[0].message,
            "Unsupported argument - An argument named \"foo\" is not expected here."
        );
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[0].source.as_deref(), Some("terraform validate"));
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].end_row, Some(3));
        assert_eq!(out[0].end_col, Some(8));
    }

    #[test]
    fn parses_warning_without_detail_or_range() {
        let stderr = br#"{
            "diagnostics": [
                { "severity": "warning", "summary": "Deprecated feature" }
            ]
        }"#;
        let out = parse(b"", stderr, 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "Deprecated feature");
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].row, None);
        assert_eq!(out[0].col, None);
    }

    #[test]
    fn valid_output_yields_nothing() {
        let stderr = br#"{"valid": true, "diagnostics": []}"#;
        assert!(parse(b"", stderr, 0).is_empty());
    }
}
