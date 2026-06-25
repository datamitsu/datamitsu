//! bslint — a brighterscript CLI tool to lint BrightScript without compiling.
//!
//! Ported from the none-ls `diagnostics/bslint` builtin. Output comes from
//! **stderr** (`from_stderr=true`); each diagnostic line matches the Lua pattern
//! `(.*):(%d+):(%d+) %- (%a+) ([%a%d]+): (.*)`:
//!
//! ```text
//! <file>:<row>:<col> - <type> <CODE>: <message>
//! ```
//!
//! e.g. `source/main.brs:10:5 - error LINT1001: Unsafe usage`. The `<type>`
//! token (alpha-only) maps to severity; `<CODE>` is alphanumeric. The filename
//! is dropped (the core knows the file). `col` is reported.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "bslint",
    description: "A brighterscript CLI tool to lint your code without compiling your project.",
    url: "https://github.com/rokucommunity/bslint",
    operations: &[Operation {
        mode: "lint",
        args: &["--files", "{file}"],
        stdin: false,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        "info" => Some(severity::INFO),
        "hint" => Some(severity::HINT),
        _ => None,
    }
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // "<file>:<row>:<col> - <type> <CODE>: <message>"
    // Lua: (.*):(%d+):(%d+) %- (%a+) ([%a%d]+): (.*)
    let dash = line.find(" - ")?;
    let (loc, rest) = (&line[..dash], &line[dash + 3..]);

    // loc = "<file>:<row>:<col>" — split the last two colon-separated numbers off.
    let (loc_rest, col_str) = loc.rsplit_once(':')?;
    let (_file, row_str) = loc_rest.rsplit_once(':')?;
    let row: u32 = row_str.parse().ok()?;
    let col: u32 = col_str.parse().ok()?;

    // rest = "<type> <CODE>: <message>"
    let colon = rest.find(": ")?;
    let head = &rest[..colon]; // "<type> <CODE>"
    let message = &rest[colon + 2..];

    let space = head.find(' ')?;
    let err_type = &head[..space];
    let code = &head[space + 1..];
    if code.is_empty() || message.is_empty() {
        return None;
    }
    // %a+ requires an alpha-only type token; %[%a%d]+ an alphanumeric code.
    if !err_type.chars().all(|c| c.is_ascii_alphabetic()) || err_type.is_empty() {
        return None;
    }
    if !code.chars().all(|c| c.is_ascii_alphanumeric()) {
        return None;
    }

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        col: Some(col),
        severity: severity_of(err_type),
        source: Some("bslint".to_string()),
        code: Some(code.to_string()),
        ..RawDiagnostic::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_line() {
        let d = parse_line("source/main.brs:10:5 - error LINT1001: Unsafe usage of m").unwrap();
        assert_eq!(d.row, Some(10));
        assert_eq!(d.col, Some(5));
        assert_eq!(d.severity, Some(severity::ERROR));
        assert_eq!(d.code.as_deref(), Some("LINT1001"));
        assert_eq!(d.source.as_deref(), Some("bslint"));
        assert_eq!(d.message, "Unsafe usage of m");
    }

    #[test]
    fn maps_warning_severity() {
        let d = parse_line("a/b.brs:3:1 - warning LINT3001: Consider using a constant").unwrap();
        assert_eq!(d.severity, Some(severity::WARNING));
        assert_eq!(d.code.as_deref(), Some("LINT3001"));
    }

    #[test]
    fn parse_reads_stderr_and_skips_noise() {
        let out = parse(
            b"",
            b"Found issues:\nsource/main.brs:10:5 - error LINT1001: bad\n\n",
            1,
        );
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].row, Some(10));
    }
}
