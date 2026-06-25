//! cfn-lint — validate AWS CloudFormation yaml/json templates.
//!
//! Ported from the none-ls `diagnostics/cfn_lint` builtin. It runs
//! `cfn-lint --format parseable` reading the template on stdin, and emits each
//! diagnostic on **stderr** (`from_stderr = true`) in the `parseable` format:
//!
//! ```text
//! <file>:<row>:<col>:<end_row>:<end_col>:<code>:<message>
//! ```
//!
//! e.g. `template.yaml:3:7:3:25:E3012:E3012: Property is not a string`. The
//! none-ls Lua pattern `:(%d+):(%d+):(%d+):(%d+):(([IEW]).*):(.*)` captures the
//! four positions, then a `code` field whose first character (`I`/`E`/`W`) is the
//! severity, then the trailing message.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "cfn_lint",
    description:
        "Validate AWS CloudFormation yaml/json templates against the AWS CloudFormation Resource Specification",
    url: "https://github.com/aws-cloudformation/cfn-lint",
    operations: &[Operation {
        mode: "lint",
        args: &["--format", "parseable"],
        stdin: true,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // cfn-lint writes parseable diagnostics to stderr (from_stderr = true).
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // The Lua pattern anchors at the first `:<digits>:`; everything before that
    // first numeric colon group is the filename (which may itself contain `:`).
    // We scan right-to-left so a path with colons can't shift the fields. The
    // suffix layout after the filename is:
    //   row : col : end_row : end_col : code : message
    // where `message` is the remainder (may contain `:`) and `code` is one field.
    let (idx, row, col, end_row, end_col) = find_positions(line)?;
    // `rest` = "<code>:<message>"
    let rest = &line[idx..];
    let colon = rest.find(':')?;
    let code = &rest[..colon];
    let message = &rest[colon + 1..];

    // Severity is the leading I/E/W of the code field; non-conforming -> skip
    // (the Lua pattern requires the code to start with [IEW]).
    let level = code.chars().next()?;
    let sev = level_severity(level)?;

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        col: Some(col),
        end_row: Some(end_row),
        end_col: Some(end_col),
        severity: Some(sev),
        code: Some(code.to_string()),
        ..RawDiagnostic::default()
    })
}

/// Locate the four `:<digits>:<digits>:<digits>:<digits>:` position fields and
/// return the byte index in `line` just past the fourth field's trailing colon,
/// along with (row, col, end_row, end_col). The four numbers are the first run
/// of four consecutive colon-separated integers — found by scanning each colon.
fn find_positions(line: &str) -> Option<(usize, u32, u32, u32, u32)> {
    let bytes = line.as_bytes();
    for (i, &b) in bytes.iter().enumerate() {
        if b != b':' {
            continue;
        }
        // Try to read exactly four ":<int>" groups starting at this colon.
        let mut pos = i;
        let mut nums = [0u32; 4];
        let mut ok = true;
        for slot in nums.iter_mut() {
            // pos points at a ':'
            if bytes.get(pos) != Some(&b':') {
                ok = false;
                break;
            }
            let start = pos + 1;
            let mut end = start;
            while end < bytes.len() && bytes[end].is_ascii_digit() {
                end += 1;
            }
            if end == start {
                ok = false;
                break;
            }
            match line[start..end].parse::<u32>() {
                Ok(n) => *slot = n,
                Err(_) => {
                    ok = false;
                    break;
                }
            }
            pos = end;
        }
        // After four numbers, the next char must be ':' (start of code field).
        if ok && bytes.get(pos) == Some(&b':') {
            return Some((pos + 1, nums[0], nums[1], nums[2], nums[3]));
        }
    }
    None
}

fn level_severity(level: char) -> Option<u8> {
    match level {
        'E' => Some(severity::ERROR),
        'W' => Some(severity::WARNING),
        'I' => Some(severity::INFO),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_line() {
        let d = parse_line("template.yaml:3:7:3:25:E3012:E3012: Property is not a string").unwrap();
        assert_eq!(d.message, "E3012: Property is not a string");
        assert_eq!((d.row, d.col), (Some(3), Some(7)));
        assert_eq!((d.end_row, d.end_col), (Some(3), Some(25)));
        assert_eq!(d.severity, Some(severity::ERROR));
        assert_eq!(d.code.as_deref(), Some("E3012"));
    }

    #[test]
    fn parses_warning_and_info() {
        let w = parse_line("t.json:1:1:1:1:W2001:W2001: Parameter Foo not used").unwrap();
        assert_eq!(w.severity, Some(severity::WARNING));
        let i = parse_line("t.json:10:5:10:9:I1001:Info hint here").unwrap();
        assert_eq!(i.severity, Some(severity::INFO));
        assert_eq!(i.code.as_deref(), Some("I1001"));
    }

    #[test]
    fn parse_reads_stderr_and_skips_noise() {
        let stderr =
            b"a.yaml:2:1:2:8:E1001:E1001: Top level template error\nnot a diagnostic line\n";
        let out = parse(b"", stderr, 1);
        assert_eq!(out.len(), 1);
        assert_eq!(out[0].code.as_deref(), Some("E1001"));
    }
}
