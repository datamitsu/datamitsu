//! selene — command line tool designed to help write correct and idiomatic Lua
//! code. Ported from the none-ls diagnostics/selene builtin.
//!
//! selene's `--display-style json2` emits NDJSON: one JSON object per line. The
//! builtin keeps only objects with `type == "Diagnostic"`, lifts the span out of
//! `primary_label.span` (`start_line`/`start_column`/`end_line`/`end_column`),
//! appends the joined `notes` to the message, and maps `severity` (`Warning` /
//! `Error`) and `code`. selene reports 0-based positions; none-ls adds an offset
//! of 1 to row/col/end_row/end_col to make them 1-based.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "selene",
    description: "Command line tool designed to help write correct and idiomatic Lua code.",
    url: "https://kampfkarren.github.io/selene/",
    operations: &[Operation {
        mode: "lint",
        args: &["--display-style", "json2", "-"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stdout)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    let trimmed = line.trim();
    if trimmed.is_empty() {
        return None;
    }
    let value: JsonValue = trimmed.parse().ok()?;
    let map = match &value {
        JsonValue::Object(m) => m,
        _ => return None,
    };

    // Only "Diagnostic" objects carry a finding; json2 also emits summary lines.
    if get_str(map, "type").as_deref() != Some("Diagnostic") {
        return None;
    }

    let mut message = get_str(map, "message")?;
    if let Some(JsonValue::Array(notes)) = map.get("notes") {
        let joined: Vec<String> = notes
            .iter()
            .filter_map(|n| match n {
                JsonValue::String(s) => Some(s.clone()),
                _ => None,
            })
            .collect();
        // The builtin always appends "\n" + join(notes, ", "), even when empty.
        message.push('\n');
        message.push_str(&joined.join(", "));
    }

    let span = map
        .get("primary_label")
        .and_then(|pl| match pl {
            JsonValue::Object(m) => m.get("span"),
            _ => None,
        })
        .and_then(|sp| match sp {
            JsonValue::Object(m) => Some(m),
            _ => None,
        });

    // none-ls applies a +1 offset to every coordinate (selene is 0-based).
    let (row, col, end_row, end_col) = match span {
        Some(sp) => (
            get_u32(sp, "start_line").map(|v| v + 1),
            get_u32(sp, "start_column").map(|v| v + 1),
            get_u32(sp, "end_line").map(|v| v + 1),
            get_u32(sp, "end_column").map(|v| v + 1),
        ),
        None => (None, None, None, None),
    };

    Some(RawDiagnostic {
        message,
        row,
        col,
        end_row,
        end_col,
        severity: get_str(map, "severity").as_deref().and_then(severity_of),
        code: get_str(map, "code"),
        ..RawDiagnostic::default()
    })
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "Error" => Some(severity::ERROR),
        "Warning" => Some(severity::WARNING),
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
        Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_diagnostic_with_span_and_notes() {
        // Two NDJSON lines: a non-Diagnostic summary plus a real Diagnostic.
        let stdout = br#"{"type":"Summary","errors":1,"warnings":0}
{"type":"Diagnostic","severity":"Error","code":"undefined_variable","message":"`foo` is not defined","notes":["help: did you mean `for`?"],"primary_label":{"span":{"start_line":2,"start_column":4,"end_line":2,"end_column":7}}}"#;
        let out = parse(stdout, b"", 1);
        assert_eq!(out.len(), 1);
        let d = &out[0];
        assert_eq!(d.message, "`foo` is not defined\nhelp: did you mean `for`?");
        assert_eq!(d.row, Some(3));
        assert_eq!(d.col, Some(5));
        assert_eq!(d.end_row, Some(3));
        assert_eq!(d.end_col, Some(8));
        assert_eq!(d.severity, Some(severity::ERROR));
        assert_eq!(d.code.as_deref(), Some("undefined_variable"));
    }

    #[test]
    fn warning_without_notes_appends_trailing_newline() {
        let stdout = br#"{"type":"Diagnostic","severity":"Warning","code":"unused_variable","message":"unused","notes":[],"primary_label":{"span":{"start_line":0,"start_column":0,"end_line":0,"end_column":3}}}"#;
        let out = parse(stdout, b"", 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "unused\n");
        assert_eq!(out[0].row, Some(1));
        assert_eq!(out[0].col, Some(1));
        assert_eq!(out[0].severity, Some(severity::WARNING));
    }

    #[test]
    fn ignores_blank_and_non_object_lines() {
        let out = parse(b"\n123\n{\"type\":\"Summary\"}\n", b"", 0);
        assert!(out.is_empty());
    }
}
