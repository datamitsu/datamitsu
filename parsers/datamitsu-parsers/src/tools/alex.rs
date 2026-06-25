//! alex — catch insensitive, inconsiderate writing.
//!
//! Ported from the none-ls `diagnostics/alex` builtin. It runs
//! `alex --stdin --quiet` and reports on **stderr**. Each diagnostic line is:
//!
//! ```text
//!   <row>:<col>-<end_row>:<end_col>  <severity>  <message>  <word>  <ruleId>
//! ```
//!
//! e.g. `  1:1-1:7   warning  Don't say "master", it may be insensitive  master-slave  retext-equality`.
//!
//! The none-ls Lua pattern is
//! `' *(%d+):(%d+)-(%d+):(%d+) *(%w+) *(.+) +[%w]+ +([-%l]+)'` with groups
//! `row, col, end_row, end_col, severity, message, code`. The message is greedy
//! up to the final two whitespace-separated tokens: a `[%w]+` word (the matched
//! source identifier, discarded) followed by the `[-%l]+` ruleId (`code`).

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "alex",
    description: "Catch insensitive, inconsiderate writing.",
    url: "https://github.com/get-alex/alex",
    operations: &[Operation {
        mode: "lint",
        args: &["--stdin", "--quiet"],
        stdin: true,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    let s = line.trim_start();

    // `<row>:<col>-<end_row>:<end_col>` followed by whitespace.
    let dash = s.find('-')?;
    let start = &s[..dash];
    let (row, col) = split_pos(start)?;

    let rest = &s[dash + 1..];
    let ws = rest.find(char::is_whitespace)?;
    let end = &rest[..ws];
    let (end_row, end_col) = split_pos(end)?;

    // After the position span: `<severity>  <message...>  <word>  <ruleId>`.
    let after = rest[ws..].trim_start();
    let sev_end = after.find(char::is_whitespace)?;
    let sev = &after[..sev_end];
    let body = after[sev_end..].trim_start();

    // The last whitespace-separated token is the ruleId ([-%l]+); the token
    // before it is the matched word ([%w]+), discarded. Everything before both
    // is the message.
    let (before_code, code) = rsplit_token(body)?;
    let (message, _word) = rsplit_token(before_code.trim_end())?;
    let message = message.trim_end().to_string();
    if message.is_empty() {
        return None;
    }

    Some(RawDiagnostic {
        message,
        row: Some(row),
        col: Some(col),
        end_row: Some(end_row),
        end_col: Some(end_col),
        severity: severity_of(sev),
        code: Some(code.to_string()),
        ..RawDiagnostic::default()
    })
}

/// `<line>:<column>` → (line, column).
fn split_pos(s: &str) -> Option<(u32, u32)> {
    let (l, c) = s.split_once(':')?;
    Some((l.trim().parse().ok()?, c.trim().parse().ok()?))
}

/// Split off the last whitespace-separated token: returns (head, last_token).
fn rsplit_token(s: &str) -> Option<(&str, &str)> {
    let s = s.trim_end();
    let idx = s.rfind(char::is_whitespace)?;
    Some((&s[..idx], s[idx..].trim_start()))
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        "info" => Some(severity::INFO),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_warning_line() {
        let line = "  1:1-1:7   warning  Don't say \"master\", it may be insensitive  master-slave  retext-equality";
        let d = parse_line(line).unwrap();
        assert_eq!(d.message, "Don't say \"master\", it may be insensitive");
        assert_eq!((d.row, d.col), (Some(1), Some(1)));
        assert_eq!((d.end_row, d.end_col), (Some(1), Some(7)));
        assert_eq!(d.severity, Some(severity::WARNING));
        assert_eq!(d.code.as_deref(), Some("retext-equality"));
    }

    #[test]
    fn parses_error_line() {
        let line = "  3:5-3:14  error  `boogeyman` may be profane  boogeyman  retext-profanities";
        let d = parse_line(line).unwrap();
        assert_eq!(d.message, "`boogeyman` may be profane");
        assert_eq!((d.row, d.col), (Some(3), Some(5)));
        assert_eq!((d.end_row, d.end_col), (Some(3), Some(14)));
        assert_eq!(d.severity, Some(severity::ERROR));
        assert_eq!(d.code.as_deref(), Some("retext-profanities"));
    }

    #[test]
    fn reads_from_stderr_and_skips_noise() {
        let stderr =
            b"some-file.md\n  1:1-1:7  warning  msg here  word  retext-equality\n  1 warning\n";
        let out = parse(b"", stderr, 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "msg here");
        assert_eq!(out[0].code.as_deref(), Some("retext-equality"));
    }
}
