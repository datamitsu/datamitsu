//! gitlint — Linter for Git commit messages. Ported from the none-ls diagnostics/gitlint builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "gitlint",
    description: "Linter for Git commit messages.",
    url: "https://jorisroovers.com/gitlint/",
    operations: &[Operation {
        mode: "lint",
        args: &["--msg-filename", "{file}"],
        stdin: false,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

// Lua pattern: (%d+): (%w+) (.+) -> groups row, code, message
fn parse_line(line: &str) -> Option<RawDiagnostic> {
    let (row_str, rest) = line.split_once(": ")?;
    let row: u32 = row_str.trim().parse().ok()?;
    if row_str.trim().is_empty() {
        return None;
    }

    let (code, message) = rest.split_once(' ')?;
    if code.is_empty() || !code.chars().all(|c| c.is_alphanumeric() || c == '_') {
        return None;
    }
    if message.is_empty() {
        return None;
    }

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        code: Some(code.to_string()),
        ..RawDiagnostic::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_gitlint_output() {
        let stderr = b"1: T1 Title exceeds max length (90>72)\n3: B5 Body message is too short (12<20)\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 2);

        assert_eq!(diags[0].row, Some(1));
        assert_eq!(diags[0].code.as_deref(), Some("T1"));
        assert_eq!(diags[0].message, "Title exceeds max length (90>72)");
        assert_eq!(diags[0].severity, None);

        assert_eq!(diags[1].row, Some(3));
        assert_eq!(diags[1].code.as_deref(), Some("B5"));
        assert_eq!(diags[1].message, "Body message is too short (12<20)");
    }

    #[test]
    fn ignores_non_matching_lines() {
        let stderr = b"some unrelated banner line\n";
        assert!(parse(b"", stderr, 0).is_empty());
    }
}
