//! clj_kondo — A linter for clojure code that sparks joy. Ported from the
//! none-ls diagnostics/clj_kondo builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "clj_kondo",
    description: "A linter for clojure code that sparks joy",
    url: "https://github.com/clj-kondo/clj-kondo",
    operations: &[Operation {
        mode: "lint",
        args: &["--cache", "--lint", "-", "--filename", "{file}"],
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
    match level {
        "error" | "Exception" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        _ => None,
    }
}

/// Ports `:(%d+):(%d+): (%w+): (.*)` — matched against the line after the
/// filename, i.e. `<file>:<row>:<col>: <severity>: <message>`. The builtin
/// skips lines beginning with "linting took ".
fn parse_line(line: &str) -> Option<RawDiagnostic> {
    if line.starts_with("linting took ") {
        return None;
    }

    // Find the ":<row>:<col>: " segment. Scan colons left-to-right and take the
    // first position where two numeric, colon-separated fields are followed by
    // "<word>: <message>".
    let mut search_from = 0;
    while let Some(rel) = line[search_from..].find(':') {
        let colon = search_from + rel;
        if let Some(d) = try_match(&line[colon + 1..]) {
            return Some(d);
        }
        search_from = colon + 1;
    }
    None
}

/// `rest` begins right after a `:`; expect `<row>:<col>: <severity>: <message>`.
fn try_match(rest: &str) -> Option<RawDiagnostic> {
    let (row_str, after_row) = take_digits(rest)?;
    let after_row = after_row.strip_prefix(':')?;
    let (col_str, after_col) = take_digits(after_row)?;
    let after_col = after_col.strip_prefix(": ")?;
    let (sev_str, after_sev) = take_word(after_col)?;
    let message = after_sev.strip_prefix(": ")?;

    Some(RawDiagnostic {
        message: message.to_string(),
        row: row_str.parse().ok(),
        col: col_str.parse().ok(),
        severity: severity_of(sev_str),
        ..RawDiagnostic::default()
    })
}

/// Splits leading ASCII digits (`%d+`); needs at least one.
fn take_digits(s: &str) -> Option<(&str, &str)> {
    let end = s.find(|c: char| !c.is_ascii_digit()).unwrap_or(s.len());
    if end == 0 {
        return None;
    }
    Some((&s[..end], &s[end..]))
}

/// Splits a leading `%w+` word (alphanumeric + underscore); needs at least one.
fn take_word(s: &str) -> Option<(&str, &str)> {
    let end = s
        .find(|c: char| !(c.is_ascii_alphanumeric() || c == '_'))
        .unwrap_or(s.len());
    if end == 0 {
        return None;
    }
    Some((&s[..end], &s[end..]))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_and_warning() {
        let stdout = b"<stdin>:3:1: error: Unresolved symbol: foo\n\
                       <stdin>:10:5: warning: unused binding x\n";
        let out = parse(stdout, b"", 3);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Unresolved symbol: foo");
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].col, Some(1));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[1].message, "unused binding x");
        assert_eq!(out[1].row, Some(10));
        assert_eq!(out[1].col, Some(5));
        assert_eq!(out[1].severity, Some(severity::WARNING));
    }

    #[test]
    fn skips_linting_took_line() {
        let stdout = b"linting took 12ms, errors: 1, warnings: 0\n\
                       <stdin>:1:1: error: boom\n";
        let out = parse(stdout, b"", 3);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "boom");
    }

    #[test]
    fn maps_exception_to_error() {
        let stdout = b"<stdin>:1:1: Exception: something blew up\n";
        let out = parse(stdout, b"", 3);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].severity, Some(severity::ERROR));
    }
}
