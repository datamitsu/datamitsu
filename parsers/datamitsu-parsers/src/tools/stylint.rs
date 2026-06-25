//! stylint — a linter for the Stylus CSS preprocessor.
//!
//! Ported from the none-ls `diagnostics/stylint` builtin. stylint prints
//! columnar, two-space-separated lines. The builtin tries two Lua patterns:
//!
//! ```text
//! ^(%d+)  (%w+)  (.+)  +%w            -> row, severity, message
//! ^(%d+):(%d+)  (%w+)  (.+)  +%w      -> row, col, severity, message
//! ```
//!
//! e.g. `10  Warning  Hashes should not...  hashEnd` or
//! `10:5  Warning  Hashes should not...  hashEnd`. The trailing `  +%w` is the
//! rule name column, which the builtin does not capture into a diagnostic field,
//! so it is dropped here. The severity token (`Warning`/`Error`) is lowercased
//! and mapped; the column is reported only by the second pattern.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "stylint",
    description: "A linter for the Stylus CSS preprocessor.",
    url: "https://github.com/SimenB/stylint",
    operations: &[Operation {
        mode: "lint",
        args: &["-"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stdout)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn severity_of(level: &str) -> Option<u8> {
    // none-ls lowercases the matched token before lookup in default_severities.
    match level.to_ascii_lowercase().as_str() {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        "information" => Some(severity::INFO),
        "hint" => Some(severity::HINT),
        _ => None,
    }
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // Both patterns require a leading digit (the row). Lines are not trimmed in
    // the Lua patterns (anchored with `^`), so leading whitespace makes a line
    // non-matching — bail unless the first char is a digit.
    if !line.starts_with(|c: char| c.is_ascii_digit()) {
        return None;
    }

    // Location column = everything up to the first double-space separator.
    let (loc, rest) = line.split_once("  ")?;

    // rest = "<severity>  <message>  +<rule>"
    let (sev, after_sev) = rest.split_once("  ")?;
    if sev.is_empty() || !sev.chars().all(|c| c.is_ascii_alphanumeric() || c == '_') {
        return None; // %w+
    }

    // The Lua `(.+)  +%w` is greedy: message is everything up to the LAST run of
    // 2+ spaces followed by a word char (the rule-name column). Find that split
    // from the right.
    let message = strip_trailing_rule(after_sev)?;
    if message.is_empty() {
        return None;
    }

    // loc is either "<row>" or "<row>:<col>".
    let (row_str, col_str) = match loc.split_once(':') {
        Some((r, c)) => (r, Some(c)),
        None => (loc, None),
    };
    let row: u32 = row_str.parse().ok()?;
    let col: Option<u32> = match col_str {
        Some(c) => Some(c.parse().ok()?),
        None => None,
    };

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        col,
        severity: severity_of(sev),
        source: Some("stylint".to_string()),
        ..RawDiagnostic::default()
    })
}

/// Port of the greedy `(.+)  +%w` tail: drop the rightmost run of 2+ spaces that
/// is followed by a word character (the rule-name column), returning the message.
fn strip_trailing_rule(s: &str) -> Option<&str> {
    let bytes = s.as_bytes();
    // Scan from the right for a position where >=2 spaces precede a word char.
    let mut i = bytes.len();
    while i > 0 {
        i -= 1;
        // Look for a word char preceded by 2+ spaces.
        let c = bytes[i] as char;
        if c.is_ascii_alphanumeric() || c == '_' {
            // count spaces immediately before i
            let mut sp = i;
            while sp > 0 && bytes[sp - 1] == b' ' {
                sp -= 1;
            }
            if i - sp >= 2 {
                let msg = &s[..sp];
                if !msg.is_empty() {
                    return Some(msg);
                }
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_row_only() {
        let d = parse_line("10  Warning  Hashes should not have a key after them  hashEnd").unwrap();
        assert_eq!(d.row, Some(10));
        assert_eq!(d.col, None);
        assert_eq!(d.severity, Some(severity::WARNING));
        assert_eq!(d.source.as_deref(), Some("stylint"));
        assert_eq!(d.message, "Hashes should not have a key after them");
    }

    #[test]
    fn parses_row_and_col() {
        let d = parse_line("12:5  Error  Missing semicolon  semicolons").unwrap();
        assert_eq!(d.row, Some(12));
        assert_eq!(d.col, Some(5));
        assert_eq!(d.severity, Some(severity::ERROR));
        assert_eq!(d.message, "Missing semicolon");
    }

    #[test]
    fn parse_skips_non_diagnostic_lines() {
        let out = parse(
            b"stylint v2.0.0\n10  Warning  bad thing happened here  zeroUnits\n\n0 Warnings\n",
            b"",
            1,
        );
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].row, Some(10));
        assert_eq!(out[0].message, "bad thing happened here");
    }
}
