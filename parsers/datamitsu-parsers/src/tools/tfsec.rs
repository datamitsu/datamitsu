//! tfsec — security scanner for Terraform code.
//! Ported from the none-ls diagnostics/tfsec builtin.
//!
//! tfsec emits `{"results":[ … ]}` with `-f json`. The builtin's custom
//! `on_output` first skips any leading noise to the first `{`, JSON-decodes, then
//! maps each result: `description` -> message, `location.start_line` -> row,
//! `location.end_line` -> end_row, `rule_id` -> code, severity pinned to warning,
//! source "tfsec". There is no column information in the output.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "tfsec",
    description: "Security scanner for Terraform code",
    url: "https://github.com/aquasecurity/tfsec",
    // Upstream runs against $DIRNAME (multiple_files), not stdin.
    operations: &[Operation {
        mode: "lint",
        args: &["-s", "-f", "json", "{file}"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    // The builtin skips any leading output before the first `{` and decodes the
    // remainder; without a `{` there is nothing parseable.
    let json = match text.find('{') {
        Some(i) => &text[i..],
        None => return Vec::new(),
    };
    let value: JsonValue = match json.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };
    let results = match &value {
        JsonValue::Object(m) => match m.get("results") {
            Some(JsonValue::Array(items)) => items,
            _ => return Vec::new(),
        },
        _ => return Vec::new(),
    };
    results.iter().filter_map(result_to_diag).collect()
}

fn result_to_diag(result: &JsonValue) -> Option<RawDiagnostic> {
    let map = match result {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    // message = description (the one required field).
    let message = get_str(map, "description")?;
    let location = map.get("location").and_then(as_object);

    Some(RawDiagnostic {
        message,
        row: location.and_then(|l| get_u32(l, "start_line")),
        end_row: location.and_then(|l| get_u32(l, "end_line")),
        code: get_str(map, "rule_id"),
        severity: Some(severity::WARNING),
        source: Some("tfsec".to_string()),
        ..RawDiagnostic::default()
    })
}

fn as_object(v: &JsonValue) -> Option<&std::collections::HashMap<String, JsonValue>> {
    match v {
        JsonValue::Object(m) => Some(m),
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
        Some(JsonValue::Number(n)) if n.is_finite() && *n >= 0.0 => Some(*n as u32),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_result() {
        let json = br#"{
            "results": [
                {
                    "rule_id": "aws-s3-enable-bucket-logging",
                    "description": "Bucket has logging disabled",
                    "location": {
                        "filename": "main.tf",
                        "start_line": 4,
                        "end_line": 6
                    }
                }
            ]
        }"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "Bucket has logging disabled");
        assert_eq!(out[0].row, Some(4));
        assert_eq!(out[0].end_row, Some(6));
        assert_eq!(out[0].code.as_deref(), Some("aws-s3-enable-bucket-logging"));
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].source.as_deref(), Some("tfsec"));
        assert_eq!(out[0].col, None);
    }

    #[test]
    fn skips_leading_noise_before_json() {
        let stdout = b"warning: something\n{\"results\":[{\"description\":\"x\",\"location\":{\"start_line\":1,\"end_line\":1}}]}";
        let out = parse(stdout, b"", 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "x");
    }

    #[test]
    fn null_results_yield_nothing() {
        assert!(parse(br#"{"results":null}"#, b"", 0).is_empty());
        assert!(parse(b"no json here", b"", 1).is_empty());
    }
}
