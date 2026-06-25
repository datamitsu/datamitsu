//! markdownlint_cli2 — fast configuration-based CLI linter for Markdown/CommonMark.
//! Ported from the none-ls diagnostics/markdownlint_cli2 builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "markdownlint_cli2",
    description: "A fast, flexible, configuration-based command-line interface for linting Markdown/CommonMark files with the markdownlint library",
    url: "https://github.com/DavidAnson/markdownlint-cli2",
    operations: &[Operation { mode: "lint", args: &["{file}"], stdin: false }],
};

/// Diagnostics are read from stderr (from_stderr = true).
pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

// Two patterns, both severity WARNING:
//   file:row:col code message
//   file:row code message
// where code is [%w-/]+ (alnum, '-', '/') and filename is %g (non-space).
fn parse_line(line: &str) -> Option<RawDiagnostic> {
    let line = line.trim_end();
    // Split off "file:row[:col]" prefix from the rest.
    let space = line.find(' ')?;
    let (locus, rest) = line.split_at(space);
    let rest = rest.trim_start();

    // rest must be "code message"
    let code_end = rest.find(' ')?;
    let code = &rest[..code_end];
    if !is_code(code) {
        return None;
    }
    let message = rest[code_end..].trim_start().to_string();
    if message.is_empty() {
        return None;
    }

    let mut parts = locus.split(':');
    let _file = parts.next()?;
    let row: u32 = parts.next()?.parse().ok()?;
    let col: Option<u32> = match parts.next() {
        Some(c) => Some(c.parse().ok()?),
        None => None,
    };
    if parts.next().is_some() {
        return None;
    }

    Some(RawDiagnostic {
        message,
        row: Some(row),
        col,
        severity: Some(severity::WARNING),
        code: Some(code.to_string()),
        ..RawDiagnostic::default()
    })
}

fn is_code(s: &str) -> bool {
    !s.is_empty()
        && s.chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '/')
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_row_col() {
        let stderr = b"README.md:3:1 MD041/first-line-heading First line in a file should be a top-level heading";
        let d = parse(b"", stderr, 1);
        assert_eq!(d.len(), 1);
        assert_eq!(d[0].row, Some(3));
        assert_eq!(d[0].col, Some(1));
        assert_eq!(d[0].code.as_deref(), Some("MD041/first-line-heading"));
        assert_eq!(d[0].severity, Some(severity::WARNING));
        assert_eq!(
            d[0].message,
            "First line in a file should be a top-level heading"
        );
    }

    #[test]
    fn parses_row_only() {
        let stderr = b"docs/guide.md:7 MD013/line-length Line length [Expected: 80; Actual: 120]";
        let d = parse(b"", stderr, 1);
        assert_eq!(d.len(), 1);
        assert_eq!(d[0].row, Some(7));
        assert_eq!(d[0].col, None);
        assert_eq!(d[0].code.as_deref(), Some("MD013/line-length"));
        assert_eq!(d[0].message, "Line length [Expected: 80; Actual: 120]");
    }

    #[test]
    fn ignores_summary_lines() {
        let stderr = b"Finding: *.md\nLinting: 1 file(s)\nSummary: 2 error(s)";
        let d = parse(b"", stderr, 1);
        assert!(d.is_empty());
    }
}
