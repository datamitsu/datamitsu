//! haml_lint — tool for writing clean and consistent HAML. Ported from the
//! none-ls diagnostics/haml_lint builtin.
//!
//! haml-lint's JSON reporter emits `{ "files": [ { "offenses": [ … ] } ] }`,
//! where each offense is `{ message, location: { line }, linter_name, severity }`.
//! The builtin walks `files[1].offenses` and maps message←message, line←location.line,
//! ruleId←linter_name, level←severity. There is no column. Severity tokens are
//! "warning" and "error".

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "haml_lint",
    description: "Tool for writing clean and consistent HAML.",
    url: "https://github.com/sds/haml-lint",
    // to_stdin=true + to_temp_file=true: stdin is piped but the tool reads a temp
    // file path ($FILENAME), so args carry {file} and stdin stays true.
    operations: &[Operation {
        mode: "lint",
        args: &["--reporter", "json", "{file}"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // from_stderr=true: diagnostics arrive on stderr; fall back to stdout.
    let bytes = if stderr.is_empty() { stdout } else { stderr };
    let text = String::from_utf8_lossy(bytes);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };

    let mut out = Vec::new();
    let Some(files) = value
        .get::<std::collections::HashMap<String, JsonValue>>()
        .and_then(|m| m.get("files"))
        .and_then(|f| f.get::<Vec<JsonValue>>())
    else {
        return out;
    };

    for file in files {
        let Some(offenses) = file
            .get::<std::collections::HashMap<String, JsonValue>>()
            .and_then(|m| m.get("offenses"))
            .and_then(|o| o.get::<Vec<JsonValue>>())
        else {
            continue;
        };
        for offense in offenses {
            if let Some(d) = from_offense(offense) {
                out.push(d);
            }
        }
    }
    out
}

fn from_offense(value: &JsonValue) -> Option<RawDiagnostic> {
    let map = value.get::<std::collections::HashMap<String, JsonValue>>()?;
    let message = map.get("message").and_then(|m| m.get::<String>())?.clone();
    let row = map
        .get("location")
        .and_then(|l| l.get::<std::collections::HashMap<String, JsonValue>>())
        .and_then(|m| m.get("line"))
        .and_then(json_u32);
    let code = map
        .get("linter_name")
        .and_then(|c| c.get::<String>())
        .cloned();
    let severity = map
        .get("severity")
        .and_then(|s| s.get::<String>())
        .and_then(|s| severity_of(s));
    Some(RawDiagnostic {
        message,
        row,
        code,
        severity,
        ..RawDiagnostic::default()
    })
}

fn json_u32(v: &JsonValue) -> Option<u32> {
    match v {
        JsonValue::Number(n) => crate::numconv::json_u32(*n),
        JsonValue::String(s) => s.trim().parse().ok(),
        _ => None,
    }
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_offenses() {
        let json = br#"{
            "files": [
                {
                    "path": "app/views/foo.haml",
                    "offenses": [
                        {
                            "severity": "warning",
                            "message": "Line is too long. [120/80]",
                            "location": { "line": 12 },
                            "linter_name": "LineLength"
                        },
                        {
                            "severity": "error",
                            "message": "Syntax error",
                            "location": { "line": 1 },
                            "linter_name": "Syntax"
                        }
                    ]
                }
            ],
            "summary": { "offense_count": 2 }
        }"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Line is too long. [120/80]");
        assert_eq!(out[0].row, Some(12));
        assert_eq!(out[0].code.as_deref(), Some("LineLength"));
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].col, None);
        assert_eq!(out[1].severity, Some(severity::ERROR));
        assert_eq!(out[1].code.as_deref(), Some("Syntax"));
    }

    #[test]
    fn empty_files_yields_nothing() {
        let out = parse(br#"{"files":[],"summary":{"offense_count":0}}"#, b"", 0);
        assert!(out.is_empty());
    }

    #[test]
    fn invalid_json_yields_nothing() {
        assert!(parse(b"not json", b"", 0).is_empty());
    }
}
