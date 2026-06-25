//! mdl — a tool to check Markdown files and flag style issues.
//! Ported from the none-ls diagnostics/mdl builtin.
//!
//! mdl `--json` emits an array of objects: `{ filename, line, rule, aliases,
//! description }`. The builtin maps row=line, code=rule, message=description and
//! hardcodes severity to WARNING (it is not present in the JSON). There is no
//! column or end position.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "mdl",
    description: "A tool to check Markdown files and flag style issues.",
    url: "https://github.com/markdownlint/markdownlint",
    operations: &[Operation { mode: "lint", args: &["--json"], stdin: true }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let attrs = Attrs {
        row: "line",
        code: "rule",
        message: "description",
        ..Attrs::defaults()
    };
    let mut out = json_diag::from_json(stdout, &attrs, severity_of);
    // The builtin hardcodes severity to warning; mdl's JSON carries no level.
    for d in &mut out {
        d.severity = Some(severity::WARNING);
    }
    out
}

fn severity_of(_level: &str) -> Option<u8> {
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_mdl_json() {
        let json = br#"[
            {"filename":"README.md","line":3,"rule":"MD013","aliases":["line-length"],"description":"Line length"},
            {"filename":"README.md","line":10,"rule":"MD009","aliases":["no-trailing-spaces"],"description":"Trailing spaces"}
        ]"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Line length");
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].code.as_deref(), Some("MD013"));
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[0].col, None);
        assert_eq!(out[1].row, Some(10));
        assert_eq!(out[1].code.as_deref(), Some("MD009"));
    }

    #[test]
    fn empty_output_yields_nothing() {
        assert!(parse(b"[]", b"", 0).is_empty());
    }
}
