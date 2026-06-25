//! write_good — English prose linter. Ported from the none-ls
//! `diagnostics/write_good` builtin.
//!
//! none-ls runs `write-good --text=$TEXT --parse` and parses each output line
//! with the Lua pattern `(%d+):(%d+):("([%w%s]+)".*)` and groups
//! `row, col, message, _quote`, plus `offsets = { col = 1 }`.
//!
//! The `message` group is everything from the opening `"` onward (the quoted
//! offending phrase followed by the rest of the explanation), e.g.
//! `"So" appears to be passive voice`. The `_quote` group is the bare phrase
//! between the quotes; it feeds the `end_col.from_quote` adapter, which locates
//! that phrase inside the **buffer content line** to derive `end_col`. The
//! buffer is a vim runtime concept not available to this WASM parser, so —
//! exactly as the Lua adapter does when no content line is present — `end_col`
//! is left unset and the Go core fills the default. `write-good` emits no
//! severity or rule code, so those stay `None` too.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "write_good",
    description: "English prose linter.",
    url: "https://github.com/btford/write-good",
    operations: &[Operation {
        mode: "lint",
        args: &["--text={file}", "--parse"],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stdout)
        .lines()
        .filter_map(parse_line)
        .collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // `<row>:<col>:<message...>` where message begins with a quoted phrase.
    let (row_str, rest) = line.split_once(':')?;
    let (col_str, message) = rest.split_once(':')?;

    let row: u32 = row_str.trim().parse().ok()?;
    let col: u32 = col_str.trim().parse().ok()?;

    // The message must start with the quoted offending phrase: `"<phrase>"...`.
    let message = message.trim_start();
    let bytes = message.as_bytes();
    if bytes.first() != Some(&b'"') {
        return None;
    }
    // Require a closing quote whose contents are alphanumerics/spaces (the
    // pattern's `[%w%s]+`), matching the upstream `_quote` capture.
    let close = message[1..].find('"')? + 1;
    let phrase = &message[1..close];
    if phrase.is_empty()
        || !phrase
            .chars()
            .all(|c| c.is_alphanumeric() || c.is_whitespace())
    {
        return None;
    }

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        // none-ls applies `offsets = { col = 1 }`.
        col: Some(col + 1),
        ..RawDiagnostic::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_passive_voice_line() {
        let line = r#"1:6:"is detected" may be passive voice"#;
        let d = parse_line(line).unwrap();
        assert_eq!(d.message, r#""is detected" may be passive voice"#);
        assert_eq!(d.row, Some(1));
        assert_eq!(d.col, Some(7)); // 6 + col offset of 1
        assert_eq!(d.end_col, None);
        assert_eq!(d.severity, None);
        assert_eq!(d.code, None);
    }

    #[test]
    fn parses_weasel_word_line() {
        let line = r#"3:0:"So" is a weasel word and can weaken meaning"#;
        let d = parse_line(line).unwrap();
        assert_eq!(d.message, r#""So" is a weasel word and can weaken meaning"#);
        assert_eq!(d.row, Some(3));
        assert_eq!(d.col, Some(1));
    }

    #[test]
    fn skips_lines_without_quoted_phrase() {
        let out = parse(b"not a diagnostic\n12:4:no quote here\n", b"", 0);
        assert!(out.is_empty());
    }
}
