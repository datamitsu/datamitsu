//! deadnix — Scan Nix files for dead code. Ported from the none-ls diagnostics/deadnix builtin.
//!
//! deadnix's `--output-format=json` emits one object per file:
//! `{"file": "...", "results": [{"line":3,"column":5,"endColumn":12,"message":"..."}]}`.
//! none-ls navigates to `output.results` and runs `from_json` over those span
//! objects with default attributes, forcing severity to WARNING. deadnix emits no
//! level token or rule id, so severity is the fixed WARNING and code stays None.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "deadnix",
    description: "Scan Nix files for dead code.",
    url: "https://github.com/astro/deadnix",
    operations: &[Operation {
        mode: "lint",
        args: &["--output-format=json", "{file}"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };

    // none-ls's `from_json` defaults; deadnix span objects use line/column/endColumn.
    // No ruleId/level fields are emitted — severity is forced to WARNING below.
    let attrs = Attrs::defaults();

    let mut out = Vec::new();
    collect(&value, &attrs, &mut out);
    out
}

/// deadnix emits one top-level object (per temp file) with a `results` array, but
/// may also stream an array of such objects. Walk either shape to the spans.
fn collect(value: &JsonValue, attrs: &Attrs, out: &mut Vec<RawDiagnostic>) {
    match value {
        JsonValue::Array(items) => {
            for it in items {
                collect(it, attrs, out);
            }
        }
        JsonValue::Object(map) => {
            if let Some(JsonValue::Array(results)) = map.get("results") {
                for span in results {
                    if let Some(mut d) = json_diag::from_obj(span, attrs, severity_of) {
                        d.severity = Some(severity::WARNING);
                        out.push(d);
                    }
                }
            }
        }
        _ => {}
    }
}

/// deadnix emits no level token; severity is fixed to WARNING by the builtin.
fn severity_of(_level: &str) -> Option<u8> {
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_dead_code_spans() {
        let json = br#"{"file":"flake.nix","results":[
            {"line":3,"column":5,"endColumn":8,"message":"Unused declaration: foo"},
            {"line":7,"column":1,"endColumn":4,"message":"Unused lambda pattern: bar"}
        ]}"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Unused declaration: foo");
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].col, Some(5));
        assert_eq!(out[0].end_col, Some(8));
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].code, None);
        assert_eq!(out[1].message, "Unused lambda pattern: bar");
    }

    #[test]
    fn handles_array_of_file_objects() {
        let json = br#"[{"file":"a.nix","results":[{"line":1,"column":2,"message":"Unused declaration: x"}]}]"#;
        let out = parse(json, b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].severity, Some(severity::WARNING));
    }

    #[test]
    fn empty_results_yield_nothing() {
        let out = parse(br#"{"file":"clean.nix","results":[]}"#, b"", 0);
        assert!(out.is_empty());
    }
}
