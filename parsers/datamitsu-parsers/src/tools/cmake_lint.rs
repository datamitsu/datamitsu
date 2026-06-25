//! cmake_lint — check cmake listfiles for style violations, common mistakes,
//! and anti-patterns.
//!
//! Ported from the none-ls `diagnostics/cmake_lint` builtin. It runs
//! `cmake-lint <file>` and reads diagnostics from **stderr**. The none-ls Lua
//! pattern is:
//!
//! ```text
//! (%d+),(%d+): %[((%w)[%d]+)%] (.+)
//! ```
//!
//! captured as `row, col, code, severity, message`. A line typically looks like:
//!
//! ```text
//! CMakeLists.txt:12,3: [C0103] Invalid function name "Foo"
//! ```
//!
//! The pattern is unanchored, so an optional `<file>:` prefix is ignored. The
//! `code` is a letter followed by digits (e.g. `C0103`); the leading letter is
//! the severity class (`E`/`W`/`C`/`R`/`I`). none-ls applies `offsets.col = 1`,
//! so the reported column is incremented by one.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "cmake_lint",
    description:
        "Check cmake listfiles for style violations, common mistakes, and anti-patterns.",
    url: "https://github.com/cheshirekow/cmake_format",
    operations: &[Operation {
        mode: "lint",
        args: &["{file}"],
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
    // Locate the "<row>,<col>: [<code>] <message>" segment. The leading
    // "<row>," digits may be preceded by an unanchored "<file>:" prefix, so scan
    // from the marker ": [" and read row/col backwards from there.
    let marker = line.find(": [")?;
    let (row, col) = row_col(&line[..marker])?;

    let rest = &line[marker + 3..]; // after ": ["
    let rb = rest.find(']')?;
    let code = &rest[..rb];
    let message = rest[rb + 1..].trim_start();
    if message.is_empty() {
        return None;
    }

    // code must be a letter followed by one or more digits.
    let mut chars = code.chars();
    let severity_letter = chars.next()?;
    if !severity_letter.is_ascii_alphabetic() {
        return None;
    }
    if code.len() < 2 || !code[1..].bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        // offsets = { col = 1 }: none-ls increments the column by one.
        col: Some(col + 1),
        severity: severity_of(severity_letter),
        code: Some(code.to_string()),
        ..RawDiagnostic::default()
    })
}

/// Read the last two `,`/`:`-separated numeric fields of the prefix as
/// `<row>,<col>` (right-to-left so a path with colons doesn't interfere).
fn row_col(prefix: &str) -> Option<(u32, u32)> {
    let (head, col) = prefix.rsplit_once(',')?;
    let col: u32 = col.trim().parse().ok()?;
    let row: u32 = head.rsplit(|c| c == ':' || c == ' ').next()?.trim().parse().ok()?;
    Some((row, col))
}

fn severity_of(letter: char) -> Option<u8> {
    match letter {
        'E' => Some(severity::ERROR),
        'W' => Some(severity::WARNING),
        'C' | 'R' | 'I' => Some(severity::INFO),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_convention_with_file_prefix() {
        let d = parse_line("CMakeLists.txt:12,3: [C0103] Invalid function name \"Foo\"").unwrap();
        assert_eq!(d.message, "Invalid function name \"Foo\"");
        assert_eq!(d.row, Some(12));
        assert_eq!(d.col, Some(4)); // 3 + offset 1
        assert_eq!(d.severity, Some(severity::INFO));
        assert_eq!(d.code.as_deref(), Some("C0103"));
    }

    #[test]
    fn parses_error_and_warning_classes() {
        let e = parse_line("5,0: [E1122] Incomplete statement").unwrap();
        assert_eq!(e.severity, Some(severity::ERROR));
        assert_eq!(e.code.as_deref(), Some("E1122"));
        assert_eq!((e.row, e.col), (Some(5), Some(1)));

        let w = parse_line("8,1: [W0101] Wrong indentation").unwrap();
        assert_eq!(w.severity, Some(severity::WARNING));
        assert_eq!(w.code.as_deref(), Some("W0101"));
    }

    #[test]
    fn non_matching_lines_are_skipped() {
        assert!(parse_line("Summary").is_none());
        assert!(parse_line("12,3: [notacode] x").is_none());
    }

    #[test]
    fn parse_reads_from_stderr() {
        let out = parse(b"", b"1,0: [C0113] a\n2,0: [E0001] b\n", 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[1].message, "b");
    }
}
