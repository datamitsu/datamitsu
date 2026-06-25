//! clazy — Qt-oriented static code analyzer based on the Clang framework.
//! Ported from the none-ls diagnostics/clazy builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "clazy",
    description: "Qt-oriented static code analyzer based on the Clang framework",
    url: "https://github.com/KDE/clazy",
    operations: &[Operation {
        mode: "lint",
        args: &[
            "--ignore-included-files",
            "--header-filter=$ROOT/.*",
            "{file}",
        ],
        stdin: false,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // from_stderr = true
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // Pattern: [^:]+:(%d+):(%d+): ([%w ]+): (.*)$
    // file:row:col: severity: message
    // Find the first ':' (end of file segment), then parse row/col/severity/message.
    let first_colon = line.find(':')?;
    let rest = &line[first_colon + 1..];

    let c1 = rest.find(':')?;
    let row: u32 = rest[..c1].parse().ok()?;
    let after_row = &rest[c1 + 1..];

    let c2 = after_row.find(':')?;
    let col: u32 = after_row[..c2].parse().ok()?;
    let after_col = &after_row[c2 + 1..];

    // after_col begins with " " then "severity: message"
    let after_col = after_col.strip_prefix(' ')?;
    let c3 = after_col.find(':')?;
    let sev_tok = &after_col[..c3];
    // severity is [%w ]+ — alphanumerics and spaces only (e.g. "fatal error")
    if sev_tok.is_empty() || !sev_tok.chars().all(|c| c.is_alphanumeric() || c == ' ') {
        return None;
    }
    let msg = after_col[c3 + 1..].strip_prefix(' ').unwrap_or(&after_col[c3 + 1..]);

    Some(RawDiagnostic {
        message: msg.to_string(),
        row: Some(row),
        col: Some(col),
        severity: severity_of(sev_tok),
        source: Some("clazy".to_string()),
        ..RawDiagnostic::default()
    })
}

fn severity_of(sev: &str) -> Option<u8> {
    match sev {
        "error" | "fatal error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        "note" | "remark" => Some(severity::INFO),
        // none-ls default is warning for unknown tokens.
        _ => Some(severity::WARNING),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_warning() {
        let stderr = b"/src/main.cpp:42:9: warning: Missing reference in range-for [-Wclazy-range-loop]\n";
        let diags = parse(b"", stderr, 0);
        assert_eq!(diags.len(), 1);
        let d = &diags[0];
        assert_eq!(d.row, Some(42));
        assert_eq!(d.col, Some(9));
        assert_eq!(d.severity, Some(severity::WARNING));
        assert_eq!(d.source.as_deref(), Some("clazy"));
        assert_eq!(
            d.message,
            "Missing reference in range-for [-Wclazy-range-loop]"
        );
    }

    #[test]
    fn parses_fatal_error_and_note() {
        let stderr = b"/src/widget.cpp:1:10: fatal error: 'QWidget' file not found\n/src/widget.cpp:5:3: note: expanded from here\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 2);
        assert_eq!(diags[0].severity, Some(severity::ERROR));
        assert_eq!(diags[0].message, "'QWidget' file not found");
        assert_eq!(diags[1].severity, Some(severity::INFO));
    }

    #[test]
    fn ignores_unmatched_lines() {
        let stderr = b"some progress output without diagnostics\n";
        assert!(parse(b"", stderr, 0).is_empty());
    }
}
