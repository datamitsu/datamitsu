//! teal — the compiler for Teal, a typed dialect of Lua. Ported from the
//! none-ls diagnostics/teal builtin.
//!
//! `tl check` emits, on stderr, section headers like `5 errors:` / `2 warnings:`
//! that set the severity for the diagnostic lines that follow, then lines of the
//! form `<file>:<row>:<col>: <message>`. Severity is stateful: the most recent
//! header applies to subsequent lines (default error). The upstream builtin also
//! filtered by temp_path and derived `end_col` from the buffer quote — both
//! require the vim runtime/buffer content, which is unavailable here, so they are
//! intentionally dropped.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "teal",
    description: "The compiler for Teal, a typed dialect of Lua.",
    url: "https://github.com/teal-language/tl",
    operations: &[Operation {
        mode: "lint",
        args: &["check", "{file}"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // from_stderr = true; fall back to stdout if stderr is empty.
    let text = if stderr.is_empty() {
        String::from_utf8_lossy(stdout)
    } else {
        String::from_utf8_lossy(stderr)
    };

    let mut out = Vec::new();
    let mut current = severity::ERROR;
    for raw in text.split('\n') {
        let line = raw.strip_suffix('\r').unwrap_or(raw);
        if let Some(sev) = parse_header(line) {
            current = sev;
            continue;
        }
        if let Some(diag) = parse_diag(line, current) {
            out.push(diag);
        }
    }
    out
}

/// `^(%d*) ([%w]+):$` — leading digits, a space, an alphanumeric word, trailing
/// colon. Returns the mapped severity when the word is a known level token.
fn parse_header(line: &str) -> Option<u8> {
    let body = line.strip_suffix(':')?;
    let (count, word) = body.split_once(' ')?;
    if !count.chars().all(|c| c.is_ascii_digit()) {
        return None;
    }
    if word.is_empty() || !word.chars().all(|c| c.is_ascii_alphanumeric()) {
        return None;
    }
    severity_of(word)
}

/// `([^:]+):(%d+):(%d+): (.*)$` — file:row:col: message.
fn parse_diag(line: &str, severity: u8) -> Option<RawDiagnostic> {
    // file = up to the first ':'
    let (file, rest) = line.split_once(':')?;
    if file.is_empty() {
        return None;
    }
    let (row_s, rest) = rest.split_once(':')?;
    let (col_s, rest) = rest.split_once(':')?;
    // message follows "col: " — require the single space after the colon.
    let message = rest.strip_prefix(' ')?;

    let row: u32 = row_s.parse().ok()?;
    let col: u32 = col_s.parse().ok()?;
    if message.is_empty() {
        return None;
    }

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        col: Some(col),
        severity: Some(severity),
        ..RawDiagnostic::default()
    })
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" | "errors" => Some(severity::ERROR),
        "warning" | "warnings" => Some(severity::WARNING),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_errors_section() {
        let stderr =
            b"2 errors:\nfoo.tl:3:10: unknown variable: x\nfoo.tl:7:1: redeclaration of 'y'\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 2);
        assert_eq!(diags[0].message, "unknown variable: x");
        assert_eq!(diags[0].row, Some(3));
        assert_eq!(diags[0].col, Some(10));
        assert_eq!(diags[0].severity, Some(severity::ERROR));
        assert_eq!(diags[1].message, "redeclaration of 'y'");
        assert_eq!(diags[1].col, Some(1));
    }

    #[test]
    fn warning_header_switches_severity() {
        let stderr = b"1 warning:\nbar.tl:5:2: unused variable z\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].severity, Some(severity::WARNING));
        assert_eq!(diags[0].row, Some(5));
    }

    #[test]
    fn defaults_to_error_without_header() {
        let stderr = b"baz.tl:1:1: syntax error\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].severity, Some(severity::ERROR));
        assert_eq!(diags[0].message, "syntax error");
    }
}
