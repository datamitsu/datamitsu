//! phpmd — PHP Mess Detector.
//!
//! Ported from the none-ls `diagnostics/phpmd` builtin (the JSON class). It runs
//! `phpmd {file} json` and emits a nested object
//! `{"files":[{"violations":[…]}]}`. Unlike the flat JSON tools, the diagnostics
//! live under `files[0].violations`, so this parser navigates there itself and
//! reuses the shared field mapping. Each violation maps `description → message`,
//! `beginLine → row`, `endLine → end_row`, `rule → code`, and a numeric
//! `priority` (1–5) → severity (1,2 → error/warning; 3 → information; 4,5 → hint).
//!
//! The builtin allows exit code ≤ 3 (phpmd uses the exit code to report whether
//! violations were found), and on a non-JSON / errored run produces a single
//! synthetic "cannot analyze/parse" diagnostic.

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "phpmd",
    description: "Runs PHP Mess Detector against PHP files.",
    url: "https://github.com/phpmd/phpmd/",
    operations: &[Operation {
        mode: "lint",
        args: &["{file}", "json"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => {
            return vec![RawDiagnostic {
                message: "phpmd error: cannot parse output as JSON.".to_string(),
                ..RawDiagnostic::default()
            }]
        }
    };

    // phpmd nests violations under files[0].violations.
    let violations = value
        .get::<std::collections::HashMap<String, JsonValue>>()
        .and_then(|root| root.get("files"))
        .and_then(|f| f.get::<Vec<JsonValue>>())
        .and_then(|files| files.first())
        .and_then(|f0| f0.get::<std::collections::HashMap<String, JsonValue>>())
        .and_then(|f0| f0.get("violations"))
        .and_then(|v| v.get::<Vec<JsonValue>>());

    let violations = match violations {
        Some(v) => v,
        None => return Vec::new(),
    };

    let attrs = Attrs {
        message: "description",
        row: "beginLine",
        end_row: "endLine",
        code: "rule",
        // priority is numeric; handled separately below.
        severity: "priority",
        ..Attrs::defaults()
    };

    violations
        .iter()
        .filter_map(|v| {
            let mut d = json_diag::from_obj(v, &attrs, |_| None)?;
            d.severity = priority_severity(v);
            // beginLine doubles as the column origin is unknown — phpmd emits only
            // line spans, so col/end_col stay None (the core supplies defaults).
            Some(d)
        })
        .collect()
}

/// Maps phpmd's numeric `priority` (1 = most severe … 5 = least) onto the 1–4
/// severity scale, matching the builtin's `severities` table.
fn priority_severity(v: &JsonValue) -> Option<u8> {
    let p = v
        .get::<std::collections::HashMap<String, JsonValue>>()?
        .get("priority")?;
    let n = match p {
        JsonValue::Number(n) => crate::numconv::json_int(*n)?,
        JsonValue::String(s) => s.trim().parse::<i64>().ok()?,
        _ => return None,
    };
    match n {
        1 => Some(severity::ERROR),
        2 => Some(severity::WARNING),
        3 => Some(severity::INFO),
        4 | 5 => Some(severity::HINT),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const SAMPLE: &[u8] = br#"{
      "files": [
        {
          "file": "/src/Foo.php",
          "violations": [
            {
              "beginLine": 10,
              "endLine": 12,
              "rule": "UnusedLocalVariable",
              "ruleSet": "Unused Code Rules",
              "priority": 3,
              "description": "Avoid unused local variables such as '$x'."
            },
            {
              "beginLine": 20,
              "endLine": 20,
              "rule": "CyclomaticComplexity",
              "priority": 1,
              "description": "The method bar() has a Cyclomatic Complexity of 11."
            }
          ]
        }
      ]
    }"#;

    #[test]
    fn parses_nested_violations_with_priority_mapping() {
        let out = parse(SAMPLE, b"", 2);
        assert_eq!(out.len(), 2);

        assert_eq!(out[0].message, "Avoid unused local variables such as '$x'.");
        assert_eq!(out[0].row, Some(10));
        assert_eq!(out[0].end_row, Some(12));
        assert_eq!(out[0].col, None);
        assert_eq!(out[0].code.as_deref(), Some("UnusedLocalVariable"));
        assert_eq!(out[0].severity, Some(severity::INFO));

        assert_eq!(out[1].row, Some(20));
        assert_eq!(out[1].code.as_deref(), Some("CyclomaticComplexity"));
        assert_eq!(out[1].severity, Some(severity::ERROR));
    }

    #[test]
    fn no_violations_yields_nothing() {
        let out = parse(br#"{"files":[]}"#, b"", 0);
        assert!(out.is_empty());
    }

    #[test]
    fn invalid_json_yields_synthetic_diagnostic() {
        let out = parse(b"not json", b"", 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "phpmd error: cannot parse output as JSON.");
    }
}
