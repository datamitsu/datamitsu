//! tidy — corrects and cleans up HTML and XML documents by fixing markup errors
//! and upgrading legacy code to modern standards. Ported from the none-ls
//! diagnostics/tidy builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "tidy",
    description: "Tidy corrects and cleans up HTML and XML documents by fixing markup errors and upgrading legacy code to modern standards.",
    url: "https://www.html-tidy.org/",
    operations: &[Operation {
        mode: "lint",
        args: &["-quiet", "-errors", "-lang", "en"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // from_stderr = true: diagnostics are emitted on stderr.
    let src = if stderr.is_empty() { stdout } else { stderr };
    String::from_utf8_lossy(src).lines().filter_map(parse_line).collect()
}

// Port of Lua pattern: line (%d+) column (%d+) %- (%a+): (.+)
fn parse_line(line: &str) -> Option<RawDiagnostic> {
    let rest = line.strip_prefix("line ")?;
    let (row_str, rest) = rest.split_once(" column ")?;
    let (col_str, rest) = rest.split_once(" - ")?;
    let (level, message) = rest.split_once(": ")?;

    // %a+ : level token must be all alphabetic.
    if level.is_empty() || !level.chars().all(|c| c.is_ascii_alphabetic()) {
        return None;
    }

    let row: u32 = row_str.parse().ok()?;
    let col: u32 = col_str.parse().ok()?;

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        col: Some(col),
        severity: severity_of(level),
        ..RawDiagnostic::default()
    })
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "Warning" => Some(severity::WARNING),
        "Error" => Some(severity::ERROR),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_warning_and_error() {
        let stderr = b"line 1 column 1 - Warning: missing <!DOCTYPE> declaration\nline 5 column 10 - Error: <foo> is not recognized!\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 2);
        assert_eq!(diags[0].row, Some(1));
        assert_eq!(diags[0].col, Some(1));
        assert_eq!(diags[0].severity, Some(severity::WARNING));
        assert_eq!(diags[0].message, "missing <!DOCTYPE> declaration");
        assert_eq!(diags[1].row, Some(5));
        assert_eq!(diags[1].col, Some(10));
        assert_eq!(diags[1].severity, Some(severity::ERROR));
        assert_eq!(diags[1].message, "<foo> is not recognized!");
    }

    #[test]
    fn ignores_non_matching_lines() {
        let stderr = b"Info: Document content looks like HTML5\nTidy found 1 warning and 0 errors!\n";
        assert!(parse(b"", stderr, 1).is_empty());
    }
}
