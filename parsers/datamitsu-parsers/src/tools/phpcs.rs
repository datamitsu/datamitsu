//! phpcs — PHP_CodeSniffer. Ported from the none-ls diagnostics/phpcs builtin.
//!
//! phpcs `--report=json` emits `{"files": {"<path>": {"messages": [...]}}}` where
//! each message is `{"message","source","severity","type","line","column",...}`.
//! none-ls navigates to `output.files[bufname].messages` and runs `from_json` with
//! `severity = "type"` (ERROR/WARNING tokens) and `code = "source"`; line/column
//! use the defaults. We iterate every file's messages (the WASM core has no single
//! buffer name to key on) and reuse the field mapping via `from_obj`.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "phpcs",
    description: "PHP_CodeSniffer is a script that tokenizes PHP, JavaScript and CSS files to detect violations of a defined coding standard.",
    url: "https://github.com/squizlabs/PHP_CodeSniffer",
    operations: &[Operation {
        mode: "lint",
        args: &[
            "--report=json",
            "-q",
            "-s",
            "--runtime-set",
            "ignore_warnings_on_exit",
            "1",
            "--runtime-set",
            "ignore_errors_on_exit",
            "1",
            "--stdin-path={file}",
            "--basepath=",
        ],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let text = String::from_utf8_lossy(stdout);
    let value: JsonValue = match text.parse() {
        Ok(v) => v,
        Err(_) => return Vec::new(),
    };

    // none-ls overrides: severity comes from the "type" token, code from "source".
    let attrs = Attrs {
        severity: "type",
        code: "source",
        ..Attrs::defaults()
    };

    let mut out = Vec::new();
    if let JsonValue::Object(root) = &value {
        if let Some(JsonValue::Object(files)) = root.get("files") {
            for file in files.values() {
                if let JsonValue::Object(fmap) = file {
                    if let Some(JsonValue::Array(messages)) = fmap.get("messages") {
                        for msg in messages {
                            if let Some(d) = json_diag::from_obj(msg, &attrs, severity_of) {
                                out.push(d);
                            }
                        }
                    }
                }
            }
        }
    }
    out
}

/// phpcs emits the level as the uppercase `type` token.
fn severity_of(level: &str) -> Option<u8> {
    match level {
        "ERROR" => Some(severity::ERROR),
        "WARNING" => Some(severity::WARNING),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_messages_across_files() {
        let json = br#"{
            "totals": {"errors": 1, "warnings": 1},
            "files": {
                "/src/foo.php": {
                    "errors": 1,
                    "warnings": 1,
                    "messages": [
                        {"message":"Missing file doc comment","source":"PEAR.Commenting.FileComment.Missing","severity":5,"type":"ERROR","line":1,"column":1},
                        {"message":"Line indented incorrectly","source":"Generic.WhiteSpace.ScopeIndent.Incorrect","severity":5,"type":"WARNING","line":12,"column":3}
                    ]
                }
            }
        }"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Missing file doc comment");
        assert_eq!(out[0].row, Some(1));
        assert_eq!(out[0].col, Some(1));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(
            out[0].code.as_deref(),
            Some("PEAR.Commenting.FileComment.Missing")
        );
        assert_eq!(out[1].severity, Some(severity::WARNING));
        assert_eq!(out[1].row, Some(12));
    }

    #[test]
    fn empty_files_yield_nothing() {
        let json = br#"{"totals":{"errors":0,"warnings":0},"files":{}}"#;
        assert!(parse(json, b"", 0).is_empty());
    }

    #[test]
    fn invalid_json_yields_nothing() {
        assert!(parse(b"phpcs status message", b"", 0).is_empty());
    }
}
