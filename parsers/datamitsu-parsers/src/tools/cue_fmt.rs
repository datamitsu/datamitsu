//! cue_fmt — reports formatting/vet errors in `.cue` files (`cue vet`).
//!
//! Ported from the none-ls `diagnostics/cue_fmt` builtin, the **multiline** class:
//! one diagnostic is spread across **two** output lines —
//!
//! ```text
//! <message>
//!     <file>:<row>:<col>
//! ```
//!
//! none-ls pairs them (even line = location, the line before = message). This is
//! exactly why the host hands the parser the **whole raw output** rather than
//! pre-splitting it per line: a line-at-a-time generator cannot pair the two.
//! `cue vet` writes to stderr, so we read stderr (falling back to stdout).

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "cue_fmt",
    description: "Reports formatting/vet errors in .cue files.",
    url: "https://github.com/cue-lang/cue",
    operations: &[Operation {
        mode: "lint",
        args: &["vet", "{file}"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // cue writes diagnostics to stderr; fall back to stdout if stderr is empty.
    let bytes = if stderr.is_empty() { stdout } else { stderr };
    let text = String::from_utf8_lossy(bytes);
    let lines: Vec<&str> = text.split('\n').collect();

    let mut out = Vec::new();
    // Pair (message, location): the location is every second line; its message is
    // the line immediately before it.
    let mut i = 1;
    while i < lines.len() {
        if let Some((row, col)) = trailing_row_col(lines[i]) {
            out.push(RawDiagnostic {
                message: lines[i - 1].trim().to_string(),
                row: Some(row),
                col: Some(col),
                end_col: Some(col + 1),
                severity: Some(severity::ERROR),
                source: Some("cue_fmt".to_string()),
                ..RawDiagnostic::default()
            });
        }
        i += 2;
    }
    out
}

/// Trailing `:<row>:<col>` of a location line, read right-to-left.
fn trailing_row_col(line: &str) -> Option<(u32, u32)> {
    let mut it = line.rsplit(':');
    let col = it.next()?.trim().parse().ok()?;
    let row = it.next()?.trim().parse().ok()?;
    Some((row, col))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pairs_message_with_following_location() {
        let stderr = b"some constraint failed\n    ./x.cue:3:5\nanother problem\n    ./x.cue:7:1\n";
        let out = parse(b"", stderr, 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "some constraint failed");
        assert_eq!((out[0].row, out[0].col), (Some(3), Some(5)));
        assert_eq!(out[0].end_col, Some(6));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[0].source.as_deref(), Some("cue_fmt"));
        assert_eq!(out[1].message, "another problem");
        assert_eq!((out[1].row, out[1].col), (Some(7), Some(1)));
    }

    #[test]
    fn reads_stdout_when_stderr_empty() {
        let out = parse(b"msg\n    f.cue:1:1\n", b"", 0);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].message, "msg");
    }

    #[test]
    fn whole_input_not_pre_split_is_required() {
        // A single concatenated payload (what the host passes) still pairs.
        let out = parse(b"", b"err one\n    a.cue:2:2", 1);
        assert_eq!(out.len(), 1);
        assert_eq!((out[0].row, out[0].col), (Some(2), Some(2)));
    }

    #[test]
    fn empty_output_yields_nothing() {
        assert!(parse(b"", b"", 0).is_empty());
    }
}
