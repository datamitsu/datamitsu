//! glslc — Shader to SPIR-V compiler. Ported from the none-ls diagnostics/glslc builtin.
//!
//! The builtin runs glslc on a temp file and reads diagnostics from stderr,
//! matching three `from_patterns` shapes (all with a lowercase severity token):
//!   1. `glslc: <severity>: ...: <message>`              (tool-level errors)
//!   2. `<file>:<row>: <severity>: <message>`            (line diagnostics)
//!   3. `<file>: <severity>: <message>`                  (file diagnostics)
//! The filename group is intentionally dropped (it's the temp file path).
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "glslc",
    description: "Shader to SPIR-V compiler.",
    url: "https://github.com/google/shaderc",
    operations: &[Operation {
        mode: "lint",
        args: &["-o", "-", "{file}"],
        stdin: false,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

/// none-ls maps severity tokens via its default `severities` table:
/// error→1, warning→2, information→3, hint→4. glslc emits `error`/`warning`.
fn severity_of(token: &str) -> Option<u8> {
    match token {
        "error" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        "information" | "info" => Some(severity::INFO),
        "hint" => Some(severity::HINT),
        _ => None,
    }
}

/// A Lua `%l+` run: one or more lowercase ASCII letters.
fn is_lower_run(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|b| b.is_ascii_lowercase())
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // Pattern 1: glslc: <severity>: <rest>: <message>
    if let Some(rest) = line.strip_prefix("glslc: ") {
        // `(%l+): .+: (.+)` — first lowercase token, then ": <middle>: <message>".
        if let Some((sev, after)) = rest.split_once(": ") {
            if is_lower_run(sev) {
                // ".+: (.+)" — require a middle segment, message is the final part.
                if let Some((_mid, message)) = after.split_once(": ") {
                    if !message.is_empty() {
                        return Some(RawDiagnostic {
                            message: message.to_string(),
                            severity: severity_of(sev),
                            ..RawDiagnostic::default()
                        });
                    }
                }
            }
        }
    }

    // Patterns 2 & 3 both start with `<file>:` where file has no `:`.
    let (file, after_file) = line.split_once(':')?;
    if file.is_empty() {
        return None;
    }

    // Pattern 2: `<file>:<row>: <severity>: <message>`
    // after_file == "<row>: <severity>: <message>"
    if let Some((row_str, rest)) = after_file.split_once(": ") {
        if let Ok(row) = row_str.parse::<u32>() {
            if let Some((sev, message)) = rest.split_once(": ") {
                if is_lower_run(sev) && !message.is_empty() {
                    return Some(RawDiagnostic {
                        message: message.to_string(),
                        row: Some(row),
                        severity: severity_of(sev),
                        ..RawDiagnostic::default()
                    });
                }
            }
        }
    }

    // Pattern 3: `<file>: <severity>: <message>`
    // after_file == " <severity>: <message>"
    let after_file = after_file.strip_prefix(' ')?;
    let (sev, message) = after_file.split_once(": ")?;
    if is_lower_run(sev) && !message.is_empty() {
        return Some(RawDiagnostic {
            message: message.to_string(),
            severity: severity_of(sev),
            ..RawDiagnostic::default()
        });
    }

    None
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn line_diagnostic() {
        let stderr = b"shader.frag:12: error: 'foo' : undeclared identifier\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].row, Some(12));
        assert_eq!(diags[0].severity, Some(severity::ERROR));
        assert_eq!(diags[0].message, "'foo' : undeclared identifier");
    }

    #[test]
    fn file_diagnostic() {
        let stderr = b"shader.frag: warning: version 460 is not yet complete\n";
        let diags = parse(b"", stderr, 0);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].row, None);
        assert_eq!(diags[0].severity, Some(severity::WARNING));
        assert_eq!(diags[0].message, "version 460 is not yet complete");
    }

    #[test]
    fn tool_level_error() {
        let stderr = b"glslc: error: shader.frag: parse error\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].severity, Some(severity::ERROR));
        assert_eq!(diags[0].message, "parse error");
        assert_eq!(diags[0].row, None);
    }
}
