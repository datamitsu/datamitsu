//! rstcheck — Checks syntax of reStructuredText and code blocks nested within it.
//! Ported from the none-ls diagnostics/rstcheck builtin.
//!
//! rstcheck writes findings to stderr in the form
//! `<file>:<row>: (<LEVEL>/<num>) <message>`, e.g.
//! `doc.rst:12: (ERROR/3) Unknown directive type "foo".`
//! The Lua pattern `([^:]+):(%d+): %((.+)/%d%) (.+)` captures
//! filename, row, severity level, and message.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "rstcheck",
    description: "Checks syntax of reStructuredText and code blocks nested within it.",
    url: "https://github.com/myint/rstcheck",
    operations: &[Operation {
        mode: "lint",
        args: &["-r", "{file}"],
        stdin: false,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // ([^:]+):(%d+): %((.+)/%d%) (.+)
    // First ":" splits filename from the rest.
    let (_file, rest) = line.split_once(':')?;
    // Next field is the row number, terminated by ": ".
    let (row_str, rest) = rest.split_once(": ")?;
    let row: u32 = row_str.trim().parse().ok()?;

    // rest begins with "(LEVEL/N) message"
    let rest = rest.strip_prefix('(')?;
    let (paren, message) = rest.split_once(") ")?;
    // paren = "LEVEL/N" — the level is everything before the last "/".
    let level = paren.rsplit_once('/').map(|(l, _)| l).unwrap_or(paren);

    if message.is_empty() {
        return None;
    }

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        severity: severity_of(level),
        ..RawDiagnostic::default()
    })
}

fn severity_of(level: &str) -> Option<u8> {
    // docutils report levels emitted by rstcheck.
    match level {
        "ERROR" | "SEVERE" => Some(severity::ERROR),
        "WARNING" => Some(severity::WARNING),
        "INFO" => Some(severity::INFO),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_line() {
        let stderr = b"doc.rst:12: (ERROR/3) Unknown directive type \"foo\".\n";
        let out = parse(b"", stderr, 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "Unknown directive type \"foo\".");
        assert_eq!(out[0].row, Some(12));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[0].col, None);
    }

    #[test]
    fn parses_levels() {
        let stderr = b"a.rst:1: (WARNING/2) Title underline too short.\n\
                       a.rst:5: (INFO/1) Hyperlink target is not referenced.\n\
                       a.rst:9: (SEVERE/4) Unexpected section title.\n";
        let out = parse(b"", stderr, 1);
        assert_eq!(out.len(), 3);
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[1].severity, Some(severity::INFO));
        assert_eq!(out[1].row, Some(5));
        assert_eq!(out[2].severity, Some(severity::ERROR));
    }

    #[test]
    fn ignores_non_matching_lines() {
        let stderr = b"some unrelated output\nWARNING: rstcheck banner\n";
        let out = parse(b"", stderr, 0);
        assert!(out.is_empty());
    }
}
