//! commitlint — checks commit messages against the conventional-commit format.
//! Ported from the none-ls diagnostics/commitlint builtin.
//!
//! Expects the `commitlint-format-json` formatter output:
//! `{"results":[{"errors":[{name,message,level},…],"warnings":[…]}]}`.
//! Each violation carries an integer `level` (1 = warning, 2 = error). The
//! builtin synthesizes a `line` from the rule name: a `body-leading-blank` rule
//! points at line 2, any other `body*` rule at line 3, everything else has no
//! line (commit-message subject diagnostics aren't positioned upstream).

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "commitlint",
    description: "commitlint checks if your commit messages meet the conventional commit format.",
    url: "https://commitlint.js.org",
    operations: &[Operation {
        mode: "lint",
        args: &["--format", "commitlint-format-json"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };

    let mut out = Vec::new();
    // Navigate to results[0]; the builtin only ever reads the first result.
    let result = value
        .get::<std::collections::HashMap<String, JsonValue>>()
        .and_then(|m| m.get("results"))
        .and_then(|r| r.get::<Vec<JsonValue>>())
        .and_then(|arr| arr.first());
    let result = match result {
        Some(JsonValue::Object(m)) => m,
        _ => return out,
    };

    for key in ["errors", "warnings"] {
        if let Some(JsonValue::Array(items)) = result.get(key) {
            for it in items {
                if let Some(d) = violation_to_diagnostic(it) {
                    out.push(d);
                }
            }
        }
    }
    out
}

fn violation_to_diagnostic(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = match value {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    let message = match map.get("message") {
        Some(JsonValue::String(s)) => s.clone(),
        _ => return None,
    };
    let name = match map.get("name") {
        Some(JsonValue::String(s)) => Some(s.as_str()),
        _ => None,
    };

    let row = name.and_then(|n| {
        if n == "body-leading-blank" {
            Some(2)
        } else if n.starts_with("body") {
            Some(3)
        } else {
            None
        }
    });

    Some(RawDiagnostic {
        message,
        row,
        severity: map.get("level").and_then(level_severity),
        code: name.map(str::to_string),
        ..RawDiagnostic::default()
    })
}

/// commitlint emits a numeric severity: 1 = warning, 2 = error.
fn level_severity(level: &JsonValue) -> Option<u8> {
    let n = match level {
        JsonValue::Number(n) => *n,
        _ => return None,
    };
    match n as i64 {
        1 => Some(severity::WARNING),
        2 => Some(severity::ERROR),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_errors_and_warnings() {
        let json = br#"{"results":[{
            "errors":[
                {"level":2,"name":"type-empty","message":"type may not be empty"},
                {"level":2,"name":"body-leading-blank","message":"body must have leading blank line"}
            ],
            "warnings":[
                {"level":1,"name":"body-max-line-length","message":"body line too long"}
            ]
        }]}"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 3);

        assert_eq!(out[0].message, "type may not be empty");
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[0].code.as_deref(), Some("type-empty"));
        assert_eq!(out[0].row, None);

        // body-leading-blank -> line 2
        assert_eq!(out[1].row, Some(2));
        // other body* rule -> line 3
        assert_eq!(out[2].row, Some(3));
        assert_eq!(out[2].severity, Some(severity::WARNING));
    }

    #[test]
    fn no_results_yields_nothing() {
        assert!(parse(br#"{"results":[]}"#, b"", 0).is_empty());
        assert!(parse(b"not json", b"", 0).is_empty());
    }
}
