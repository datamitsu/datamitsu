//! pmd — An extensible cross-language static code analyzer. Ported from the
//! none-ls diagnostics/pmd builtin.
//!
//! PMD emits a nested JSON report (`{files:[{violations:[…]}]}`), so it cannot
//! use the flat `json_diag` helper — we navigate the structure directly. Each
//! violation supplies begin/end positions, a `ruleset/rule` code, a description
//! message, and a numeric `priority` mapped to severity via
//! `max(1, priority - 1)` (PMD priority 1 is highest → ERROR).

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "pmd",
    description: "An extensible cross-language static code analyzer.",
    url: "https://pmd.github.io",
    operations: &[Operation { mode: "lint", args: &["--format", "json", "--dir", "{file}"], stdin: false }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let mut out = Vec::new();
    let files = match &value {
        JsonValue::Object(m) => match m.get("files") {
            Some(JsonValue::Array(files)) => files,
            _ => return out,
        },
        _ => return out,
    };
    for file in files {
        let violations = match file {
            JsonValue::Object(m) => match m.get("violations") {
                Some(JsonValue::Array(vs)) => vs,
                _ => continue,
            },
            _ => continue,
        };
        for v in violations {
            if let Some(d) = violation_to_diag(v) {
                out.push(d);
            }
        }
    }
    out
}

fn violation_to_diag(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = match value {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    let message = get_str(map, "description")?;
    Some(RawDiagnostic {
        message,
        row: get_u32(map, "beginline"),
        col: get_u32(map, "begincolumn"),
        end_row: get_u32(map, "endline"),
        end_col: get_u32(map, "endcolumn").map(|c| c + 1),
        code: code_of(map),
        // PMD priority: 1 (highest) → ERROR. none-ls uses max(1, priority - 1).
        severity: get_u32(map, "priority").map(|p| p.saturating_sub(1).max(1) as u8),
        ..RawDiagnostic::default()
    })
}

fn code_of(map: &std::collections::HashMap<String, JsonValue>) -> Option<String> {
    let ruleset = get_str(map, "ruleset")?;
    let rule = get_str(map, "rule")?;
    Some(format!("{ruleset}/{rule}"))
}

fn get_str(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<String> {
    match map.get(key) {
        Some(JsonValue::String(s)) => Some(s.clone()),
        _ => None,
    }
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
    use crate::severity;

    #[test]
    fn parses_nested_violations() {
        let json = br#"{
            "files": [
                {
                    "filename": "Foo.java",
                    "violations": [
                        {
                            "beginline": 10,
                            "begincolumn": 5,
                            "endline": 10,
                            "endcolumn": 20,
                            "ruleset": "Best Practices",
                            "rule": "UnusedLocalVariable",
                            "priority": 3,
                            "description": "Avoid unused local variables such as 'x'."
                        }
                    ]
                }
            ]
        }"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "Avoid unused local variables such as 'x'.");
        assert_eq!(out[0].row, Some(10));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].end_row, Some(10));
        assert_eq!(out[0].end_col, Some(21)); // endcolumn + 1
        assert_eq!(out[0].code.as_deref(), Some("Best Practices/UnusedLocalVariable"));
        assert_eq!(out[0].severity, Some(severity::WARNING)); // max(1, 3-1) = 2
    }

    #[test]
    fn priority_one_maps_to_error() {
        let json = br#"{"files":[{"violations":[
            {"beginline":1,"begincolumn":1,"priority":1,"ruleset":"R","rule":"X","description":"boom"}
        ]}]}"#;
        let out = parse(json, b"", 0);
        assert_eq!(out[0].severity, Some(severity::ERROR)); // max(1, 1-1) = 1
    }

    #[test]
    fn invalid_json_yields_nothing() {
        assert!(parse(b"not json", b"", 0).is_empty());
    }
}
