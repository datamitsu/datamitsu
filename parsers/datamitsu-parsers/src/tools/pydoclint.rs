//! pydoclint — Python docstring linter checking docstring sections against
//! signatures. Ported from the none-ls diagnostics/pydoclint builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "pydoclint",
    description: "Pydoclint is a Python docstring linter to check whether a docstring's sections (arguments, returns, raises, ...) match the function signature or function implementation. To see all violation codes go to [pydoclint](https://jsh9.github.io/pydoclint/violation_codes.html)",
    url: "https://github.com/jsh9/pydoclint",
    // generator_opts: to_temp_file + from_stderr; diagnostics read from stderr.
    operations: &[Operation {
        mode: "lint",
        args: &[
            "--show-filenames-in-every-violation-message=true",
            "-q",
            "{file}",
        ],
        stdin: false,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    String::from_utf8_lossy(stderr)
        .lines()
        .filter_map(parse_line)
        .collect()
}

// Upstream Lua pattern (file prefix escaped): `<path>:(%d+): (DOC%d+: .*)`
// e.g. `rel/path/to/file.py:42: DOC101: Docstring contains fewer arguments...`
fn parse_line(line: &str) -> Option<RawDiagnostic> {
    // Find the trailing `:<row>: DOC...` segment. The DOC marker disambiguates
    // from any colons inside the path.
    let doc_idx = line.find(": DOC")?;
    let (head, rest) = line.split_at(doc_idx);
    // head ends with `:<row>` ; rest starts with `: ` then `DOC<digits>: ...`
    let row_str = head.rsplit(':').next()?;
    if row_str.is_empty() || !row_str.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let row: u32 = row_str.parse().ok()?;

    let message = rest.strip_prefix(": ")?;
    // message begins with `DOC<digits>: ...`; require the DOC code shape.
    let code_end = message.find(':')?;
    let code = &message[..code_end];
    if !code.starts_with("DOC") || !code[3..].bytes().all(|b| b.is_ascii_digit()) || code.len() <= 3
    {
        return None;
    }

    Some(RawDiagnostic {
        message: message.to_string(),
        row: Some(row),
        ..RawDiagnostic::default()
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_violation() {
        let stderr =
            b"src/foo.py:42: DOC101: Docstring contains fewer arguments than in function signature.\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].row, Some(42));
        assert_eq!(
            diags[0].message,
            "DOC101: Docstring contains fewer arguments than in function signature."
        );
        assert_eq!(diags[0].severity, None);
        assert_eq!(diags[0].col, None);
    }

    #[test]
    fn ignores_non_matching_lines() {
        let stderr = b"Loading config...\nsrc/a.py:7: DOC201: does not have a return section\n";
        let diags = parse(b"", stderr, 1);
        assert_eq!(diags.len(), 1);
        assert_eq!(diags[0].row, Some(7));
    }
}
