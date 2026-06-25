//! qmllint — verifies the syntactic validity of QML files. Ported from the
//! none-ls diagnostics/qmllint builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "qmllint",
    description: "qmllint is a tool shipped with Qt that verifies the syntactic validity of QML files. It also warns about some QML anti-patterns.",
    url: "https://doc-snapshots.qt.io/qt6-dev/qtquick-tools-and-utilities.html#qmllint",
    operations: &[Operation { mode: "lint", args: &["{file}"], stdin: false }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    // Diagnostics are emitted on stderr (from_stderr = true).
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

// Errorformat layout from the builtin:
//   "%trror: %f:%l:%c: %m"   -> "Error: <file>:<line>:<col>: <message>"
//   "%tarning: %f:%l:%c: %m" -> "Warning: <file>:<line>:<col>: <message>"
// %t captures the first character of the level word ("E" / "W").
fn parse_line(line: &str) -> Option<RawDiagnostic> {
    let (severity, rest) = if let Some(rest) = line.strip_prefix("Error: ") {
        (Some(severity::ERROR), rest)
    } else if let Some(rest) = line.strip_prefix("Warning: ") {
        (Some(severity::WARNING), rest)
    } else {
        return None;
    };

    // rest = "<file>:<line>:<col>: <message>"
    let (location, message) = rest.split_once(": ")?;
    let mut parts = location.rsplitn(3, ':');
    let col = parts.next()?.parse::<u32>().ok()?;
    let row = parts.next()?.parse::<u32>().ok()?;
    // remaining part is the file path (unused — core associates with the file)
    parts.next()?;

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        col: Some(col),
        severity,
        ..RawDiagnostic::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_error_and_warning() {
        let stderr = b"Error: /path/to/Main.qml:10:5: Unexpected token\nWarning: /path/to/Main.qml:12:3: Unqualified access\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 2);

        assert_eq!(diags[0].message, "Unexpected token");
        assert_eq!(diags[0].row, Some(10));
        assert_eq!(diags[0].col, Some(5));
        assert_eq!(diags[0].severity, Some(severity::ERROR));

        assert_eq!(diags[1].message, "Unqualified access");
        assert_eq!(diags[1].row, Some(12));
        assert_eq!(diags[1].col, Some(3));
        assert_eq!(diags[1].severity, Some(severity::WARNING));
    }

    #[test]
    fn ignores_unmatched_lines() {
        let stderr = b"some unrelated output\nError: a.qml:1:1: boom\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].message, "boom");
    }
}
