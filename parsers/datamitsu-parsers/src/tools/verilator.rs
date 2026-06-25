//! verilator — Verilog and SystemVerilog linter powered by Verilator.
//! Ported from the none-ls diagnostics/verilator builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "verilator",
    description: "Verilog and SystemVerilog linter power by Verilator",
    url: "https://www.veripool.org/verilator/",
    operations: &[Operation {
        mode: "lint",
        args: &["-lint-only", "-Wno-fatal", "{file}"],
        stdin: false,
    }],
};

// Diagnostics come from stderr (from_stderr = true). Upstream Lua pattern:
//   %%(%w+).*<bufname>:(%d+):(%d+): (.*)
// e.g. "%Error: top.sv:10:5: syntax error". We match the leading "%<Word>"
// severity token, then the trailing "<file>:row:col: message" tail; the path
// segment between is matched loosely (".*") since the bufname is not known here.
pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // Must begin with "%<Word>" (e.g. %Error, %Warning).
    let rest = line.strip_prefix('%')?;
    let sev_end = rest.find(|c: char| !c.is_alphanumeric())?;
    if sev_end == 0 {
        return None;
    }
    let sev_token = &rest[..sev_end];

    // Find the trailing "<file>:row:col: message" by scanning from the right.
    // Locate the ": " separating "col:" from the message.
    let (locus, message) = split_locus(rest)?;
    let (row, col) = parse_row_col(locus)?;

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        col: Some(col),
        severity: severity_of(sev_token),
        ..RawDiagnostic::default()
    })
}

/// Splits a tail like "...:10:5: syntax error" into ("...:10:5", "syntax error").
fn split_locus(s: &str) -> Option<(&str, &str)> {
    // Find the right-most ":<digits>:<digits>: " by working back from message.
    // We look for the ": " that follows the col number.
    let bytes = s.as_bytes();
    let mut i = 0;
    let mut best: Option<usize> = None;
    while let Some(pos) = s[i..].find(": ") {
        let abs = i + pos;
        // Require the char immediately before ": " to be a digit (col number).
        if abs > 0 && bytes[abs - 1].is_ascii_digit() {
            best = Some(abs);
        }
        i = abs + 1;
    }
    let abs = best?;
    Some((&s[..abs], &s[abs + 2..]))
}

/// Parses the trailing ":row:col" out of "<file>:row:col".
fn parse_row_col(locus: &str) -> Option<(u32, u32)> {
    let (head, col_str) = locus.rsplit_once(':')?;
    let (_, row_str) = head.rsplit_once(':')?;
    let col = col_str.parse().ok()?;
    let row = row_str.parse().ok()?;
    Some((row, col))
}

fn severity_of(token: &str) -> Option<u8> {
    match token {
        "Error" => Some(severity::ERROR),
        "Warning" => Some(severity::WARNING),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_and_warning() {
        let stderr = b"%Error: top.sv:10:5: syntax error, unexpected ';'\n\
                       %Warning-WIDTH: top.sv:22:13: Operator has a width mismatch\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 2);

        assert_eq!(diags[0].message, "syntax error, unexpected ';'");
        assert_eq!(diags[0].row, Some(10));
        assert_eq!(diags[0].col, Some(5));
        assert_eq!(diags[0].severity, Some(severity::ERROR));

        assert_eq!(diags[1].row, Some(22));
        assert_eq!(diags[1].col, Some(13));
        // "Warning-WIDTH" is not exactly "Warning"; the %w+ token is "Warning".
        assert_eq!(diags[1].severity, Some(severity::WARNING));
        assert_eq!(diags[1].message, "Operator has a width mismatch");
    }

    #[test]
    fn ignores_non_diagnostic_lines() {
        let stderr = b"some banner line without locus\n";
        assert!(parse(b"", stderr, 0).is_empty());
    }
}
