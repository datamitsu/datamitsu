//! mlint — linter for MATLAB files. Ported from the none-ls `diagnostics/mlint`
//! builtin.
//!
//! mlint runs on a real file (temp file with `$FILENAME`, not stdin — MATLAB
//! rejects filenames containing `-`) and emits diagnostics on **stderr**, one
//! per line, of the form:
//!
//! ```text
//! L 5 (C 10): message text
//! ```
//!
//! The none-ls Lua pattern is `L (%d+) .C (%d+).*: (.*)`: `L`, the row, a single
//! char then `C`, the column, then anything up to the first `: ` and the message
//! as the remainder. No severity/code are emitted, so both stay `None`.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "mlint",
    description: "Linter for MATLAB files",
    url: "https://www.mathworks.com/help/matlab/ref/mlint.html",
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
    // Pattern: L (%d+) .C (%d+).*: (.*)
    let rest = line.strip_prefix("L ")?;
    // row = leading digits
    let row_end = rest.find(|c: char| !c.is_ascii_digit())?;
    let row: u32 = rest[..row_end].parse().ok()?;
    let rest = &rest[row_end..];

    // " .C " — a space, one char, 'C', a space (Lua `.C ` after the space).
    // The Lua pattern is `L (%d+) .C (%d+)`: after the row there is a space,
    // then any single char, then 'C', then a space.
    let cidx = rest.find('C')?;
    // Require at least the single wildcard char before 'C' plus a trailing space.
    if cidx < 1 {
        return None;
    }
    let after_c = &rest[cidx + 1..];
    let after_c = after_c.strip_prefix(' ')?;
    let col_end = after_c
        .find(|c: char| !c.is_ascii_digit())
        .unwrap_or(after_c.len());
    let col: u32 = after_c[..col_end].parse().ok()?;
    let tail = &after_c[col_end..];

    // .*: (.*) — message is everything after the first ": ".
    let sep = tail.find(": ")?;
    let message = tail[sep + 2..].to_string();
    if message.is_empty() {
        return None;
    }

    Some(RawDiagnostic {
        message,
        row: Some(row),
        col: Some(col),
        ..RawDiagnostic::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_row_col_message() {
        let stderr = b"L 5 (C 10): Terminate statement with semicolon to suppress output.\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].row, Some(5));
        assert_eq!(diags[0].col, Some(10));
        assert_eq!(
            diags[0].message,
            "Terminate statement with semicolon to suppress output."
        );
        assert_eq!(diags[0].severity, None);
        assert_eq!(diags[0].code, None);
    }

    #[test]
    fn ignores_non_matching_lines() {
        let stderr = b"some banner line\nL 12 (C 3): The value assigned here is never used.\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].row, Some(12));
        assert_eq!(diags[0].col, Some(3));
    }
}
