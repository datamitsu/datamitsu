//! twigcs — Runs Twigcs against Twig files. Ported from the none-ls
//! diagnostics/twigcs builtin.
//!
//! Twigcs emits `{"files":[{"violations":[...]}],...}`. Each violation carries
//! `line`, `column`, a human `message`, and a NUMERIC `severity` (1/2/3). The
//! builtin maps that numeric index through a custom severities list
//! `{information, warning, error, hint}` — i.e. 1→info, 2→warning, 3→error,
//! 4→hint. Because the severity is a number (not a string token) and the
//! diagnostics are nested under `files[1].violations`, this needs a bespoke
//! navigator rather than the shared `json_diag::from_json` string path.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "twigcs",
    description: "Runs Twigcs against Twig files.",
    url: "https://github.com/friendsoftwig/twigcs",
    // to_temp_file=true -> no stdin, $FILENAME becomes {file}.
    operations: &[Operation {
        mode: "lint",
        args: &["--reporter", "json", "{file}"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };

    // Navigate output.files[0].violations (none-ls uses files[1] = first in Lua).
    let violations = value
        .get::<std::collections::HashMap<String, JsonValue>>()
        .and_then(|root| root.get("files"))
        .and_then(|files| files.get::<Vec<JsonValue>>())
        .and_then(|files| files.first())
        .and_then(|f| f.get::<std::collections::HashMap<String, JsonValue>>())
        .and_then(|f| f.get("violations"))
        .and_then(|v| v.get::<Vec<JsonValue>>());

    let mut out = Vec::new();
    if let Some(items) = violations {
        for it in items {
            if let Some(d) = violation_to_diag(it) {
                out.push(d);
            }
        }
    }
    out
}

fn violation_to_diag(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = value.get::<std::collections::HashMap<String, JsonValue>>()?;
    let message = match map.get("message") {
        Some(JsonValue::String(s)) => s.clone(),
        _ => return None,
    };
    Some(RawDiagnostic {
        message,
        row: num_field(map, "line"),
        col: num_field(map, "column"),
        severity: map.get("severity").and_then(severity_of),
        ..RawDiagnostic::default()
    })
}

fn num_field(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
    match map.get(key) {
        Some(JsonValue::Number(n)) if n.is_finite() && *n >= 0.0 => Some(*n as u32),
        _ => None,
    }
}

/// Twigcs reports a numeric severity used as an index into the builtin's
/// `{information, warning, error, hint}` list: 1→info, 2→warning, 3→error,
/// 4→hint. Anything else leaves severity unset.
fn severity_of(value: &JsonValue) -> Option<u8> {
    let n = match value {
        JsonValue::Number(n) => *n,
        _ => return None,
    };
    match n as i64 {
        1 => Some(severity::INFO),
        2 => Some(severity::WARNING),
        3 => Some(severity::ERROR),
        4 => Some(severity::HINT),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_nested_violations() {
        let json = br#"{
            "failures": 1,
            "files": [
                {
                    "file": "template.twig",
                    "violations": [
                        {"line": 3, "column": 5, "severity": 3, "message": "The spread operator should be used."},
                        {"line": 7, "column": 1, "severity": 2, "message": "A print statement should start with one space."}
                    ]
                }
            ]
        }"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "The spread operator should be used.");
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[1].severity, Some(severity::WARNING));
        assert_eq!(out[1].row, Some(7));
    }

    #[test]
    fn empty_violations_yields_nothing() {
        let json = br#"{"files":[{"file":"a.twig","violations":[]}]}"#;
        assert!(parse(json, b"", 0).is_empty());
    }

    #[test]
    fn info_severity_maps_to_info() {
        let json = br#"{"files":[{"violations":[{"line":1,"column":1,"severity":1,"message":"note"}]}]}"#;
        let out = parse(json, b"", 1);
        assert_eq!(out[0].severity, Some(severity::INFO));
    }

    #[test]
    fn invalid_json_yields_nothing() {
        assert!(parse(b"not json", b"", 1).is_empty());
    }
}
