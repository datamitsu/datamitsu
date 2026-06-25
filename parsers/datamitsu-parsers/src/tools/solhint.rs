//! solhint — Solidity linter (security + style). Ported from the none-ls diagnostics/solhint builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "solhint",
    description: "An open source project for linting Solidity code. It provides both security and style guide validations.",
    url: "https://protofire.github.io/solhint/",
    operations: &[Operation {
        mode: "lint",
        args: &["{file}", "--formatter", "unix"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stdout)
        .lines()
        .filter_map(parse_line)
        .collect()
}

// Pattern: ([^:]*):([%d]+):([%d]+): (.*) %[([%a]+)/([%a%p]+)%]
// e.g. "contracts/Foo.sol:12:5: Avoid using inline assembly [Warning/no-inline-assembly]"
fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // filename: up to the first colon ([^:]*)
    let (_filename, rest) = line.split_once(':')?;

    // row: digits up to next colon
    let (row_str, rest) = rest.split_once(':')?;
    if row_str.is_empty() || !row_str.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let row: u32 = row_str.parse().ok()?;

    // col: digits up to ": " (note the space after the colon)
    let (col_str, rest) = rest.split_once(": ")?;
    if col_str.is_empty() || !col_str.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let col: u32 = col_str.parse().ok()?;

    // remainder: "<message> [<severity>/<code>]"
    let rest = rest.trim_end();
    if !rest.ends_with(']') {
        return None;
    }
    let open = rest.rfind(" [")?;
    let message = rest[..open].to_string();
    let bracket = &rest[open + 2..rest.len() - 1]; // strip " [" and "]"
    let (sev_token, code) = bracket.split_once('/')?;

    Some(RawDiagnostic {
        message,
        row: Some(row),
        col: Some(col),
        severity: severity_of(sev_token),
        code: Some(code.to_string()),
        ..RawDiagnostic::default()
    })
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "Error" => Some(severity::ERROR),
        "Warning" => Some(severity::WARNING),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_warning_and_error() {
        let out = b"contracts/Foo.sol:12:5: Avoid using inline assembly [Warning/no-inline-assembly]\ncontracts/Foo.sol:3:1: Compiler version must be fixed [Error/compiler-version]\n";
        let diags = parse(out, b"", 1);
        assert_eq!(diags.len(), 2);

        assert_eq!(diags[0].message, "Avoid using inline assembly");
        assert_eq!(diags[0].row, Some(12));
        assert_eq!(diags[0].col, Some(5));
        assert_eq!(diags[0].severity, Some(severity::WARNING));
        assert_eq!(diags[0].code.as_deref(), Some("no-inline-assembly"));

        assert_eq!(diags[1].message, "Compiler version must be fixed");
        assert_eq!(diags[1].severity, Some(severity::ERROR));
        assert_eq!(diags[1].code.as_deref(), Some("compiler-version"));
    }

    #[test]
    fn ignores_non_matching_lines() {
        let out = b"3 problems (1 error, 2 warnings)\n";
        assert!(parse(out, b"", 1).is_empty());
    }
}
