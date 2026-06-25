//! npm_groovy_lint — Lint, format and auto-fix Groovy, Jenkinsfile, and Gradle
//! files. Ported from the none-ls diagnostics/npm_groovy_lint builtin.
//!
//! npm-groovy-lint's `-o json` output is a single object whose `files` map holds
//! one entry per linted file; only the first file's `errors` array is consumed
//! (mirroring the builtin's `vim.tbl_keys(...)[1]`). Each error carries
//! `msg`/`rule`/`severity`/`line`, plus an optional `range` whose
//! `start`/`end.{line,character}` become the span — with `endColumn` forced to 0
//! when the range straddles multiple lines, exactly as the builtin does. The flat
//! `json_diag::from_json` mapper can't reach the nested shape, so we navigate with
//! `tinyjson`.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "npm_groovy_lint",
    description: "Lint, format and auto-fix Groovy, Jenkinsfile, and Gradle files.",
    url: "https://github.com/nvuillam/npm-groovy-lint",
    // to_temp_file=true: npm-groovy-lint reads a real path ($FILENAME), not stdin.
    operations: &[Operation {
        mode: "lint",
        args: &["--failon", "none", "-o", "json", "{file}"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let root = match as_obj(&value) {
        Some(m) => m,
        None => return Vec::new(),
    };
    let files = match root.get("files").and_then(as_obj) {
        Some(m) => m,
        None => return Vec::new(),
    };
    // Builtin takes only the first file key. HashMap iteration order is arbitrary,
    // but npm-groovy-lint lints a single file per invocation here, so there is one.
    let file = match files.values().next().and_then(as_obj) {
        Some(m) => m,
        None => return Vec::new(),
    };
    let errors = match file.get("errors") {
        Some(JsonValue::Array(items)) => items,
        _ => return Vec::new(),
    };
    errors.iter().filter_map(from_error).collect()
}

fn from_error(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = as_obj(value)?;
    let message = get_str(map, "msg")?;
    let mut diag = RawDiagnostic {
        message,
        row: get_u32_v(map.get("line")),
        code: get_str(map, "rule"),
        severity: get_str(map, "severity").and_then(|s| severity_of(&s)),
        ..RawDiagnostic::default()
    };
    if let Some(range) = map.get("range").and_then(as_obj) {
        let start = range.get("start").and_then(as_obj);
        let end = range.get("end").and_then(as_obj);
        let start_line = start.and_then(|s| get_u32_v(s.get("line")));
        let end_line = end.and_then(|e| get_u32_v(e.get("line")));
        diag.row = start_line;
        diag.end_row = end_line;
        diag.col = start.and_then(|s| get_u32_v(s.get("character")));
        // Multi-line range -> endColumn 0; same line -> end character.
        diag.end_col = if start_line != end_line {
            Some(0)
        } else {
            end.and_then(|e| get_u32_v(e.get("character")))
        };
    }
    Some(diag)
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        "info" => Some(severity::INFO),
        _ => None,
    }
}

fn as_obj(value: &JsonValue) -> Option<&HashMap<String, JsonValue>> {
    match value {
        JsonValue::Object(m) => Some(m),
        _ => None,
    }
}

fn get_str(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
    match map.get(key) {
        Some(JsonValue::String(s)) => Some(s.clone()),
        _ => None,
    }
}

fn get_u32_v(v: Option<&JsonValue>) -> Option<u32> {
    match v {
        Some(JsonValue::Number(n)) if n.is_finite() && *n >= 0.0 => Some(*n as u32),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_without_range() {
        let json = br#"{
            "files": {
                "0": {
                    "errors": [
                        {
                            "id": 1,
                            "line": 7,
                            "rule": "UnnecessarySemicolon",
                            "severity": "warning",
                            "msg": "Semicolons as line endings can be removed safely"
                        }
                    ]
                }
            }
        }"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "Semicolons as line endings can be removed safely");
        assert_eq!(out[0].row, Some(7));
        assert_eq!(out[0].col, None);
        assert_eq!(out[0].code.as_deref(), Some("UnnecessarySemicolon"));
        assert_eq!(out[0].severity, Some(severity::WARNING));
    }

    #[test]
    fn same_line_range_keeps_end_character() {
        let json = br#"{
            "files": {
                "f": {
                    "errors": [
                        {
                            "line": 3,
                            "rule": "SpaceAfterComma",
                            "severity": "error",
                            "msg": "Missing space",
                            "range": {
                                "start": { "line": 3, "character": 10 },
                                "end": { "line": 3, "character": 14 }
                            }
                        }
                    ]
                }
            }
        }"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].end_row, Some(3));
        assert_eq!(out[0].col, Some(10));
        assert_eq!(out[0].end_col, Some(14));
        assert_eq!(out[0].severity, Some(severity::ERROR));
    }

    #[test]
    fn multi_line_range_zeroes_end_column() {
        let json = br#"{
            "files": {
                "f": {
                    "errors": [
                        {
                            "line": 5,
                            "rule": "ClassSize",
                            "severity": "info",
                            "msg": "Class is too large",
                            "range": {
                                "start": { "line": 5, "character": 0 },
                                "end": { "line": 40, "character": 1 }
                            }
                        }
                    ]
                }
            }
        }"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].row, Some(5));
        assert_eq!(out[0].end_row, Some(40));
        assert_eq!(out[0].col, Some(0));
        assert_eq!(out[0].end_col, Some(0));
        assert_eq!(out[0].severity, Some(severity::INFO));
    }

    #[test]
    fn invalid_and_empty_yield_nothing() {
        assert!(parse(b"not json", b"", 1).is_empty());
        assert!(parse(br#"{"files":{}}"#, b"", 0).is_empty());
    }
}
