//! proselint — An English prose linter. Ported from the none-ls
//! diagnostics/proselint builtin.
//!
//! proselint's JSON is nested and custom, so this does not use `json_diag`:
//! `output.result` is a map of file → `{ diagnostics: [ ... ] }`, and each
//! diagnostic carries `pos` ([line, col]), `span` ([start_col, end_col]),
//! `check_path` (the rule), and `message`. The builtin bails out when the
//! top-level `output.error` is set and otherwise hardcodes severity to warning
//! (proselint no longer emits a per-diagnostic level).

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "proselint",
    description: "An English prose linter.",
    url: "https://github.com/amperser/proselint",
    operations: &[Operation {
        mode: "lint",
        args: &["check", "--output-format=json"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let root = match &value {
        JsonValue::Object(m) => m,
        _ => return Vec::new(),
    };

    // Builtin: `if output.error then return diags end`.
    if matches!(root.get("error"), Some(v) if !matches!(v, JsonValue::Null)) {
        return Vec::new();
    }

    let result = match root.get("result") {
        Some(JsonValue::Object(m)) => m,
        _ => return Vec::new(),
    };

    let mut out = Vec::new();
    for file_output in result.values() {
        let file_obj = match file_output {
            JsonValue::Object(m) => m,
            _ => continue,
        };
        let diags = match file_obj.get("diagnostics") {
            Some(JsonValue::Array(a)) => a,
            _ => continue,
        };
        for d in diags {
            if let Some(diag) = from_diag(d) {
                out.push(diag);
            }
        }
    }
    out
}

fn from_diag(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = match value {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    let message = match map.get("message") {
        Some(JsonValue::String(s)) => s.clone(),
        _ => return None,
    };

    // pos = [line, col]
    let pos = array_of(map, "pos");
    let row = pos.and_then(|a| number_at(a, 0));
    let col = pos.and_then(|a| number_at(a, 1));

    // span = [start_col, end_col]; the builtin only takes span[2] (end_col).
    let end_col = array_of(map, "span").and_then(|a| number_at(a, 1));

    let code = match map.get("check_path") {
        Some(JsonValue::String(s)) => Some(s.clone()),
        _ => None,
    };

    Some(RawDiagnostic {
        message,
        row,
        col,
        end_col,
        code,
        // proselint no longer includes a severity -> the builtin chooses warning.
        severity: Some(severity::WARNING),
        ..RawDiagnostic::default()
    })
}

fn array_of<'a>(
    map: &'a std::collections::HashMap<String, JsonValue>,
    key: &str,
) -> Option<&'a Vec<JsonValue>> {
    match map.get(key) {
        Some(JsonValue::Array(a)) => Some(a),
        _ => None,
    }
}

fn number_at(arr: &[JsonValue], idx: usize) -> Option<u32> {
    match arr.get(idx) {
        Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_nested_diagnostics() {
        let json = br#"{
            "status": "success",
            "result": {
                "stdin": {
                    "diagnostics": [
                        {
                            "check_path": "typography.symbols.curly_quotes",
                            "message": "Use the curly quote.",
                            "pos": [3, 5],
                            "span": [5, 12]
                        }
                    ]
                }
            }
        }"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "Use the curly quote.");
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].end_col, Some(12));
        assert_eq!(
            out[0].code.as_deref(),
            Some("typography.symbols.curly_quotes")
        );
        assert_eq!(out[0].severity, Some(severity::WARNING));
    }

    #[test]
    fn top_level_error_yields_nothing() {
        let json = br#"{"error": "boom", "result": {"f": {"diagnostics": [{"message": "x", "pos": [1, 1], "span": [1, 2]}]}}}"#;
        assert!(parse(json, b"", 0).is_empty());
    }

    #[test]
    fn invalid_json_yields_nothing() {
        assert!(parse(b"not json", b"", 1).is_empty());
    }
}
