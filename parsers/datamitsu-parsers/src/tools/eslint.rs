//! eslint — the pluggable JavaScript/TypeScript linter.
//!
//! eslint is NOT a none-ls diagnostics builtin (it was moved to an external
//! plugin), so this is ported directly from eslint's `--format json` output: an
//! array of result objects, each with a `messages` array. Two wrinkles make it a
//! bespoke parser rather than a `json_diag::from_json` one-liner:
//!   * diagnostics are **nested** under each file's `messages`, and
//!   * `severity` is **numeric** (2 = error, 1 = warning), not a string token;
//!   * `ruleId` may be `null` (parse/internal errors) → no code.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "eslint",
    description: "Pluggable linter for JavaScript and TypeScript.",
    url: "https://eslint.org",
    operations: &[Operation {
        mode: "lint",
        args: &["--format", "json", "{file}"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let results = match &value {
        JsonValue::Array(a) => a,
        _ => return Vec::new(),
    };
    let mut out = Vec::new();
    for result in results {
        let obj = match result {
            JsonValue::Object(m) => m,
            _ => continue,
        };
        if let Some(JsonValue::Array(messages)) = obj.get("messages") {
            for msg in messages {
                if let Some(d) = message_to_diag(msg) {
                    out.push(d);
                }
            }
        }
    }
    out
}

fn message_to_diag(msg: &JsonValue) -> Option<RawDiagnostic> {
    let m = match msg {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    let message = match m.get("message") {
        Some(JsonValue::String(s)) => s.clone(),
        _ => return None,
    };
    Some(RawDiagnostic {
        message,
        row: num(m, "line"),
        col: num(m, "column"),
        end_row: num(m, "endLine"),
        end_col: num(m, "endColumn"),
        severity: severity_of(m.get("severity")),
        code: match m.get("ruleId") {
            Some(JsonValue::String(s)) => Some(s.clone()),
            _ => None, // null for parse/internal errors
        },
        ..RawDiagnostic::default()
    })
}

fn num(m: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
    match m.get(key) {
        Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
        _ => None,
    }
}

fn severity_of(v: Option<&JsonValue>) -> Option<u8> {
    match v {
        Some(JsonValue::Number(n)) => match crate::numconv::json_int(*n) {
            Some(2) => Some(severity::ERROR),
            Some(1) => Some(severity::WARNING),
            _ => None,
        },
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    // Real `eslint --format json` output (trimmed).
    const SAMPLE: &[u8] = br#"[{"filePath":"/x/broken.js","messages":[
        {"ruleId":"no-unused-vars","severity":2,"message":"'x' is assigned a value but never used.","line":1,"column":5,"endLine":1,"endColumn":6},
        {"ruleId":"semi","severity":1,"message":"Missing semicolon.","line":1,"column":10,"endLine":2,"endColumn":1},
        {"ruleId":null,"severity":2,"message":"Parsing error: Unexpected token","line":3,"column":1}
    ]}]"#;

    #[test]
    fn parses_nested_messages_with_numeric_severity() {
        let out = parse(SAMPLE, b"", 1);
        assert_eq!(out.len(), 3);

        assert_eq!(out[0].message, "'x' is assigned a value but never used.");
        assert_eq!((out[0].row, out[0].col), (Some(1), Some(5)));
        assert_eq!((out[0].end_row, out[0].end_col), (Some(1), Some(6)));
        assert_eq!(out[0].severity, Some(severity::ERROR)); // eslint 2 -> error
        assert_eq!(out[0].code.as_deref(), Some("no-unused-vars"));

        assert_eq!(out[1].severity, Some(severity::WARNING)); // eslint 1 -> warning
        assert_eq!(out[1].code.as_deref(), Some("semi"));

        // null ruleId -> no code; still a diagnostic.
        assert_eq!(out[2].code, None);
        assert_eq!(out[2].message, "Parsing error: Unexpected token");
    }

    #[test]
    fn clean_run_yields_nothing() {
        assert!(parse(br#"[{"filePath":"/x/ok.js","messages":[]}]"#, b"", 0).is_empty());
    }
}
