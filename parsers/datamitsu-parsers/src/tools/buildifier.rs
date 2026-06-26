//! buildifier — formatter/linter for bazel BUILD, WORKSPACE and .bzl files.
//! Ported from the none-ls diagnostics/buildifier builtin.
//!
//! Output shape (`-format=json`): `{"files":[{"filename":…,"warnings":[{"start":
//! {"line","column"},"end":{"line","column"},"message","url"}]}]}`. none-ls picks
//! the file matching the buffer name; since the core lints one file at a time we
//! flatten every file's warnings. On a parse failure buildifier prints to stderr
//! as `<path>:<line>:<col>: <message>`, which none-ls turns into one error.
use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "buildifier",
    description:
        "buildifier is a tool for formatting and linting bazel BUILD, WORKSPACE, and .bzl files.",
    url: "https://github.com/bazelbuild/buildtools/tree/master/buildifier",
    operations: &[Operation {
        mode: "lint",
        args: &["-mode=check", "-lint=warn", "-format=json", "-path={file}"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    if let Ok(JsonValue::Object(root)) = text.parse::<JsonValue>() {
        if let Some(JsonValue::Array(files)) = root.get("files") {
            let mut out = Vec::new();
            for file in files {
                if let JsonValue::Object(f) = file {
                    if let Some(JsonValue::Array(warnings)) = f.get("warnings") {
                        for w in warnings {
                            if let Some(d) = parse_warning(w) {
                                out.push(d);
                            }
                        }
                    }
                }
            }
            return out;
        }
    }

    // Parse-error path: buildifier could not produce JSON and reported on stderr.
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_error_line)
        .collect()
}

fn parse_warning(value: &JsonValue) -> Option<RawDiagnostic> {
    let obj = match value {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    // none-ls only emits a warning when both start and end are present.
    let (row, col) = pos(obj.get("start")?)?;
    let (end_row, end_col) = pos(obj.get("end")?)?;
    let base = str_field(obj, "message")?;
    let message = match str_field(obj, "url") {
        Some(url) => format!("{base} ({url})"),
        None => base,
    };
    Some(RawDiagnostic {
        message,
        row: Some(row),
        col: Some(col),
        end_row: Some(end_row),
        end_col: Some(end_col),
        severity: Some(severity::WARNING),
        source: Some("buildifier".to_string()),
        ..RawDiagnostic::default()
    })
}

fn pos(value: &JsonValue) -> Option<(u32, u32)> {
    let obj = match value {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    Some((u32_field(obj, "line")?, u32_field(obj, "column")?))
}

fn str_field(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<String> {
    match map.get(key) {
        Some(JsonValue::String(s)) => Some(s.clone()),
        _ => None,
    }
}

fn u32_field(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
    match map.get(key) {
        Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
        _ => None,
    }
}

/// Lua pattern `.-:(%d+):(%d+): (.*)` — last `:line:col: message` on the line.
fn parse_error_line(line: &str) -> Option<RawDiagnostic> {
    let (loc, message) = split_after_loc(line)?;
    let mut it = loc.rsplitn(3, ':');
    let col: u32 = it.next()?.trim().parse().ok()?;
    let row: u32 = it.next()?.trim().parse().ok()?;
    Some(RawDiagnostic {
        message: message.trim().to_string(),
        row: Some(row),
        col: Some(col),
        severity: Some(severity::ERROR),
        source: Some("buildifier".to_string()),
        ..RawDiagnostic::default()
    })
}

/// Split `prefix:line:col: message` into (`prefix:line:col`, `message`) at the
/// first `": "` that follows two numeric `:`-delimited groups.
fn split_after_loc(line: &str) -> Option<(&str, &str)> {
    // Find ": " separators and test each as the message boundary.
    let bytes = line.as_bytes();
    for i in 0..bytes.len().saturating_sub(1) {
        if bytes[i] == b':' && bytes[i + 1] == b' ' {
            let loc = &line[..i];
            // loc must end with :line:col (two trailing numeric groups).
            let mut it = loc.rsplitn(3, ':');
            let col = it.next();
            let row = it.next();
            if let (Some(c), Some(r)) = (col, row) {
                if !c.is_empty()
                    && c.bytes().all(|b| b.is_ascii_digit())
                    && !r.is_empty()
                    && r.bytes().all(|b| b.is_ascii_digit())
                {
                    return Some((loc, &line[i + 2..]));
                }
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_json_warnings() {
        let json = br#"{"files":[{"filename":"BUILD","warnings":[
            {"start":{"line":10,"column":1},"end":{"line":10,"column":20},
             "message":"Loaded symbol is unused.","url":"https://example.com/unused-load"}
        ]}]}"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].row, Some(10));
        assert_eq!(out[0].col, Some(1));
        assert_eq!(out[0].end_row, Some(10));
        assert_eq!(out[0].end_col, Some(20));
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].source.as_deref(), Some("buildifier"));
        assert_eq!(
            out[0].message,
            "Loaded symbol is unused. (https://example.com/unused-load)"
        );
    }

    #[test]
    fn parses_stderr_parse_error() {
        let out = parse(b"", b"/path/to/BUILD:12:5: syntax error near foo\n", 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].row, Some(12));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[0].message, "syntax error near foo");
    }

    #[test]
    fn empty_when_no_warnings() {
        let out = parse(br#"{"files":[{"filename":"BUILD","warnings":[]}]}"#, b"", 0);
        assert!(out.is_empty());
    }
}
