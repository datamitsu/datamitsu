//! erb_lint — Lint your ERB or HTML files. Ported from the none-ls
//! diagnostics/erb_lint builtin.
//!
//! erb_lint emits a nested JSON document `{ "files": [ { "offenses": [...] } ] }`.
//! The builtin reads only the first file's offenses; each offense carries a
//! `message`, a `linter` (rule id), and a `location` with start/last line+column.
//! `endColumn` is `last_column + 1` (none-ls makes the span end exclusive). The
//! tool emits no severity token, so `severity` stays `None`.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "erb_lint",
    description: "Lint your ERB or HTML files",
    url: "https://github.com/Shopify/erb-lint",
    operations: &[Operation {
        mode: "lint",
        args: &["--format", "json", "--stdin", "{file}"],
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
    let Some(files) = get_array(&value, "files") else {
        return out;
    };
    let Some(first) = files.first() else {
        return out;
    };
    let Some(offenses) = get_array(first, "offenses") else {
        return out;
    };
    for off in offenses {
        if let Some(d) = from_offense(off) {
            out.push(d);
        }
    }
    out
}

// tinyjson stores objects as HashMap<String, JsonValue>; alias for readability.
type HashMapJson = std::collections::HashMap<String, JsonValue>;

fn get_array<'a>(value: &'a JsonValue, key: &str) -> Option<&'a Vec<JsonValue>> {
    let map: &HashMapJson = value.get()?;
    match map.get(key)? {
        JsonValue::Array(a) => Some(a),
        _ => None,
    }
}

fn from_offense(value: &JsonValue) -> Option<RawDiagnostic> {
    let map: &HashMapJson = value.get()?;
    let message = get_str(map, "message")?;
    let code = get_str(map, "linter");

    let (mut row, mut col, mut end_row, mut end_col) = (None, None, None, None);
    if let Some(JsonValue::Object(loc)) = map.get("location") {
        row = get_u32(loc, "start_line");
        col = get_u32(loc, "start_column");
        end_row = get_u32(loc, "last_line");
        // none-ls makes the end column exclusive: last_column + 1.
        end_col = get_u32(loc, "last_column").map(|c| c + 1);
    }

    Some(RawDiagnostic {
        message,
        row,
        col,
        end_row,
        end_col,
        code,
        ..RawDiagnostic::default()
    })
}

fn get_str(map: &HashMapJson, key: &str) -> Option<String> {
    match map.get(key) {
        Some(JsonValue::String(s)) => Some(s.clone()),
        _ => None,
    }
}

fn get_u32(map: &HashMapJson, key: &str) -> Option<u32> {
    match map.get(key) {
        Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_offense_from_first_file() {
        let json = br#"{
            "files": [
                {
                    "path": "app/views/x.html.erb",
                    "offenses": [
                        {
                            "linter": "SpaceInHtmlTag",
                            "message": "Extra space detected where there should be no space.",
                            "location": {
                                "start_line": 2,
                                "start_column": 5,
                                "last_line": 2,
                                "last_column": 6
                            }
                        }
                    ]
                }
            ]
        }"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "Extra space detected where there should be no space.");
        assert_eq!(out[0].code.as_deref(), Some("SpaceInHtmlTag"));
        assert_eq!(out[0].row, Some(2));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].end_row, Some(2));
        assert_eq!(out[0].end_col, Some(7)); // last_column 6 + 1
        assert_eq!(out[0].severity, None); // erb_lint emits no level
    }

    #[test]
    fn no_offenses_yields_nothing() {
        let json = br#"{"files":[{"path":"a.erb","offenses":[]}]}"#;
        assert!(parse(json, b"", 0).is_empty());
    }

    #[test]
    fn invalid_json_yields_nothing() {
        assert!(parse(b"not json", b"", 1).is_empty());
    }
}
