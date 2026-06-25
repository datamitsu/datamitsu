//! cppcheck — a tool for fast static analysis of C/C++ code.
//!
//! Ported from the none-ls `diagnostics/cppcheck` builtin. It runs cppcheck with
//! `--template=gcc`, reads from **stderr**, and each diagnostic line looks like:
//!
//! ```text
//! <file>:<row>:<col>: <severity>: <message>
//! ```
//!
//! e.g. `main.c:5:7: error: Array 'a[10]' accessed at index 10, which is out of bounds.`
//!
//! The none-ls Lua pattern `(%d+):(%d+): (%w+): (.*)` matches the `row:col: sev: msg`
//! tail anywhere in the line (so a path with colons doesn't break parsing). Severity
//! mapping (with none-ls's default-to-error fallback): error→error, warning→warning,
//! note→info, style→hint, performance→warning, portability→info.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "cppcheck",
    description: "A tool for fast static analysis of C/C++ code.",
    url: "https://github.com/danmar/cppcheck",
    operations: &[Operation {
        mode: "lint",
        args: &[
            "--enable=warning,style,performance,portability",
            "--template=gcc",
            "{file}",
        ],
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
    // Find the "row:col: severity: " tail. Scan colon positions and try to match
    // `<digits>:<digits>: <word>: <message>` starting after each "<digits>:<digits>".
    let bytes = line.as_bytes();
    let mut search_from = 0;
    while let Some(rel) = line[search_from..].find(':') {
        let first_colon = search_from + rel;
        // row digits end at first_colon; find their start (contiguous digits).
        let mut row_start = first_colon;
        while row_start > 0 && bytes[row_start - 1].is_ascii_digit() {
            row_start -= 1;
        }
        if row_start < first_colon {
            if let Some(diag) = try_match(line, row_start, first_colon) {
                return Some(diag);
            }
        }
        search_from = first_colon + 1;
    }
    None
}

/// Try to parse `<row>:<col>: <severity>: <message>` where `line[row_start..first_colon]`
/// is the row digit run and `first_colon` is the colon after the row.
fn try_match(line: &str, row_start: usize, first_colon: usize) -> Option<RawDiagnostic> {
    let row: u32 = line[row_start..first_colon].parse().ok()?;

    // col digits immediately after the first colon.
    let rest = &line[first_colon + 1..];
    let col_len = rest.bytes().take_while(|b| b.is_ascii_digit()).count();
    if col_len == 0 {
        return None;
    }
    let col: u32 = rest[..col_len].parse().ok()?;
    let rest = &rest[col_len..];

    // ": " then severity word, then ": " then message.
    let rest = rest.strip_prefix(": ")?;
    let sev_len = rest
        .bytes()
        .take_while(|b| b.is_ascii_alphanumeric() || *b == b'_')
        .count();
    if sev_len == 0 {
        return None;
    }
    let severity_token = &rest[..sev_len];
    let rest = &rest[sev_len..];
    let message = rest.strip_prefix(": ")?;

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        col: Some(col),
        severity: severity_of(severity_token),
        ..RawDiagnostic::default()
    })
}

/// none-ls maps known tokens, falling back to ERROR for anything unrecognized.
fn severity_of(token: &str) -> Option<u8> {
    Some(match token {
        "error" => severity::ERROR,
        "warning" => severity::WARNING,
        "note" => severity::INFO,
        "style" => severity::HINT,
        "performance" => severity::WARNING,
        "portability" => severity::INFO,
        _ => severity::ERROR,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_with_path_containing_colons() {
        let d = parse_line(
            "src/main.c:5:7: error: Array 'a[10]' accessed at index 10, which is out of bounds.",
        )
        .unwrap();
        assert_eq!(d.row, Some(5));
        assert_eq!(d.col, Some(7));
        assert_eq!(d.severity, Some(severity::ERROR));
        assert_eq!(
            d.message,
            "Array 'a[10]' accessed at index 10, which is out of bounds."
        );
    }

    #[test]
    fn maps_style_to_hint_and_portability_to_info() {
        let style = parse_line("a.cpp:1:1: style: The scope of the variable 'i' can be reduced.")
            .unwrap();
        assert_eq!(style.severity, Some(severity::HINT));

        let port = parse_line("a.cpp:2:3: portability: Non reentrant function used.").unwrap();
        assert_eq!(port.severity, Some(severity::INFO));
    }

    #[test]
    fn parse_reads_stderr_and_collects() {
        let stderr = b"x.c:10:2: warning: Possible null pointer dereference.\nx.c:11:4: performance: Prefer prefix.\n";
        let out = parse(b"", stderr, 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].severity, Some(severity::WARNING));
        assert_eq!(out[1].severity, Some(severity::WARNING));
        assert_eq!(out[1].row, Some(11));
    }

    #[test]
    fn non_matching_line_is_skipped() {
        assert!(parse_line("Checking src/main.c ...").is_none());
    }
}
