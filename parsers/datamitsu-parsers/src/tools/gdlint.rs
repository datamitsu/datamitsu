//! gdlint — static analysis linter for GDScript. Ported from the none-ls diagnostics/gdlint builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "gdlint",
    description:
        "A linter that performs a static analysis on gdscript code according to some predefined configuration.",
    url: "https://github.com/Scony/godot-gdscript-toolkit",
    operations: &[Operation { mode: "lint", args: &["{file}"], stdin: false }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // from_stderr = true in the builtin.
    String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Ported Lua pattern: ":(%d+): (.*)" capturing row and message.
fn parse_line(line: &str) -> Option<RawDiagnostic> {
    let colon = line.find(':')?;
    let rest = &line[colon + 1..];
    let next_colon = rest.find(':')?;
    let row: u32 = rest[..next_colon].trim().parse().ok()?;
    let message = rest[next_colon + 1..].trim().to_string();
    if message.is_empty() {
        return None;
    }
    Some(RawDiagnostic { message, row: Some(row), ..RawDiagnostic::default() })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_row_and_message() {
        let stderr = b"player.gd:12: Function argument name is not valid\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].row, Some(12));
        assert_eq!(diags[0].message, "Function argument name is not valid");
        assert_eq!(diags[0].col, None);
        assert_eq!(diags[0].severity, None);
    }

    #[test]
    fn ignores_non_matching_lines() {
        let stderr = b"Success: no problems found\nfoo.gd:3: unused variable\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].row, Some(3));
        assert_eq!(diags[0].message, "unused variable");
    }
}
