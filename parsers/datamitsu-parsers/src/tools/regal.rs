//! regal — a linter for Rego. Ported from the none-ls diagnostics/regal builtin.
//!
//! regal emits a JSON object `{"violations":[…]}` (on stderr; the builtin sets
//! `from_stderr = true`). Each violation carries `description`, `level`, `title`
//! (used as the code) and a nested `location` object with `row`, `col` and
//! optional `text`. The builtin derives `end_col = location.text:len() + 1` when
//! `text` is present, else `0`. A violation without a `location` is skipped.
//! Unknown/absent levels fall back to error (`severities[d.level] or severities.error`).

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "regal",
    description: "Regal is a linter for Rego, with the goal of making your Rego magnificent!.",
    url: "https://docs.styra.com/regal",
    operations: &[Operation { mode: "lint", args: &["lint", "-f", "json", "{file}"], stdin: false }],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // `from_stderr = true`: regal writes its JSON report to stderr. Fall back to
    // stdout if stderr is empty, to be forgiving.
    let stderr = String::from_utf8_lossy(stderr);
    let stdout = String::from_utf8_lossy(stdout);
    let output = if stderr.trim().is_empty() { stdout.as_ref() } else { stderr.as_ref() };

    let decoded: JsonValue = match output.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };

    let violations = match &decoded {
        JsonValue::Object(map) => match map.get("violations") {
            Some(JsonValue::Array(items)) => items,
            _ => return Vec::new(),
        },
        _ => return Vec::new(),
    };

    violations.iter().filter_map(from_violation).collect()
}

fn from_violation(violation: &JsonValue) -> Option<RawDiagnostic> {
    let map = match violation {
        JsonValue::Object(m) => m,
        _ => return None,
    };
    // A violation without a location is skipped (mirrors `if d.location ~= nil`).
    let location = match map.get("location") {
        Some(JsonValue::Object(loc)) => loc,
        _ => return None,
    };
    let message = match map.get("description") {
        Some(JsonValue::String(s)) => s.clone(),
        _ => return None,
    };
    // end_col = #text + 1 when location.text is present, else 0.
    let end_col = match location.get("text") {
        Some(JsonValue::String(t)) => (t.chars().count() as u32) + 1,
        _ => 0,
    };
    Some(RawDiagnostic {
        message,
        row: get_u32(location, "row"),
        col: get_u32(location, "col"),
        end_col: Some(end_col),
        severity: Some(severity_of(get_str(map, "level").as_deref())),
        source: Some("regal".to_string()),
        code: get_str(map, "title"),
        ..RawDiagnostic::default()
    })
}

/// Maps regal's `level` token to the 1–4 scale; unknown/absent → error
/// (`severities[d.level] or severities.error`).
fn severity_of(level: Option<&str>) -> u8 {
    match level {
        Some("error") => severity::ERROR,
        Some("warning") => severity::WARNING,
        Some("information") => severity::INFO,
        Some("hint") => severity::HINT,
        _ => severity::ERROR,
    }
}

fn get_str(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
    match map.get(key) {
        Some(JsonValue::String(s)) => Some(s.clone()),
        _ => None,
    }
}

fn get_u32(map: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
    match map.get(key) {
        Some(JsonValue::Number(n)) if n.is_finite() && *n >= 0.0 => Some(*n as u32),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_violations_with_location() {
        let json = br#"{"violations":[
            {"description":"Prefer snake_case for names","level":"error","title":"prefer-snake-case",
             "category":"style","location":{"row":3,"col":5,"file":"policy.rego","text":"camelCase := 1"}},
            {"description":"Avoid importing input","level":"warning","title":"avoid-importing-input",
             "category":"imports","location":{"row":10,"col":1,"file":"policy.rego"}}
        ]}"#;
        let out = parse(b"", json, 0);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Prefer snake_case for names");
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].end_col, Some("camelCase := 1".len() as u32 + 1));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[0].source.as_deref(), Some("regal"));
        assert_eq!(out[0].code.as_deref(), Some("prefer-snake-case"));
        // No text => end_col 0.
        assert_eq!(out[1].end_col, Some(0));
        assert_eq!(out[1].severity, Some(severity::WARNING));
    }

    #[test]
    fn unknown_level_defaults_to_error() {
        let json = br#"{"violations":[
            {"description":"x","title":"r","location":{"row":1,"col":1}}
        ]}"#;
        let out = parse(b"", json, 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].severity, Some(severity::ERROR));
    }

    #[test]
    fn violation_without_location_is_skipped() {
        let json = br#"{"violations":[{"description":"orphan","level":"error","title":"r"}]}"#;
        let out = parse(b"", json, 0);
        assert!(out.is_empty());
    }
}
