//! tsc — the TypeScript compiler's type diagnostics.
//!
//! Also covers **tsgo** (the native-Go tsc port): same output format, so point a
//! `tsgo` tool's `outputParser` at "tsc". Ported from `tsc --noEmit --pretty
//! false` output:
//!
//! ```text
//! file(row,col): severity TScode: message
//! ```
//!
//! e.g. `src/x.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.`

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "tsc",
    description: "TypeScript compiler type diagnostics (also covers tsgo).",
    url: "https://www.typescriptlang.org",
    operations: &[Operation {
        mode: "lint",
        // --pretty false keeps the format stable (no color/leading gutter).
        args: &["--noEmit", "--pretty", "false"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let bytes = if stdout.is_empty() { stderr } else { stdout };
    String::from_utf8_lossy(bytes).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // Location "file(row,col)" ends at the first "): ".
    let close = line.find("): ")?;
    let open = line[..close].rfind('(')?;
    let pos = &line[open + 1..close]; // "row,col"
    let comma = pos.find(',')?;
    let row: u32 = pos[..comma].trim().parse().ok()?;
    let col: u32 = pos[comma + 1..].trim().parse().ok()?;

    // After the location: "severity TScode: message".
    let rest = &line[close + 3..];
    let colon = rest.find(": ")?;
    let head = &rest[..colon]; // "error TS2322"
    let message = rest[colon + 2..].trim().to_string();
    let (sev_tok, code) = head.split_once(' ').unwrap_or((head, ""));
    Some(RawDiagnostic {
        message,
        row: Some(row),
        col: Some(col),
        severity: severity_of(sev_tok),
        code: if code.is_empty() { None } else { Some(code.to_string()) },
        ..RawDiagnostic::default()
    })
}

fn severity_of(s: &str) -> Option<u8> {
    match s {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_with_code_and_position() {
        let d = parse_line("src/x.ts(1,7): error TS2322: Type 'string' is not assignable to type 'number'.")
            .unwrap();
        assert_eq!((d.row, d.col), (Some(1), Some(7)));
        assert_eq!(d.severity, Some(severity::ERROR));
        assert_eq!(d.code.as_deref(), Some("TS2322"));
        assert_eq!(d.message, "Type 'string' is not assignable to type 'number'.");
    }

    #[test]
    fn skips_summary_and_unmatched_lines() {
        assert!(parse_line("Found 2 errors in 1 file.").is_none());
        assert!(parse_line("").is_none());
    }

    #[test]
    fn collects_multiple() {
        let out = parse(
            b"a.ts(2,23): error TS2304: Cannot find name 'z'.\nb.ts(9,1): warning TS6133: 'x' is declared but never used.\n",
            b"",
            1,
        );
        assert_eq!(out.len(), 2);
        assert_eq!(out[1].severity, Some(severity::WARNING));
        assert_eq!(out[1].code.as_deref(), Some("TS6133"));
    }
}
