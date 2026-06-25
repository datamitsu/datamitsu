//! pylint — Python static code analysis tool. Ported from the none-ls
//! diagnostics/pylint builtin.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "pylint",
    description: "Pylint is a Python static code analysis tool which looks for programming errors, helps enforcing a coding standard, sniffs for code smells and offers simple refactoring suggestions.",
    url: "https://github.com/PyCQA/pylint",
    operations: &[Operation {
        mode: "lint",
        args: &["--from-stdin", "{file}", "-f", "json"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let attrs = Attrs {
        row: "line",
        col: "column",
        code: "symbol",
        severity: "type",
        ..Attrs::defaults()
    };
    json_diag::from_json(stdout, &attrs, severity_of)
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "error" | "fatal" => Some(severity::ERROR),
        "warning" => Some(severity::WARNING),
        // none-ls maps convention/refactor to "information"; pylint's "info" maps there too.
        "convention" | "refactor" | "info" | "information" => Some(severity::INFO),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_pylint_json() {
        let json = br#"[
            {"type":"convention","module":"foo","obj":"","line":1,"column":0,
             "path":"foo.py","symbol":"missing-module-docstring",
             "message":"Missing module docstring","message-id":"C0114"},
            {"type":"error","module":"foo","obj":"","line":3,"column":4,
             "path":"foo.py","symbol":"undefined-variable",
             "message":"Undefined variable 'x'","message-id":"E0602"}
        ]"#;
        let out = parse(json, b"", 4);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Missing module docstring");
        assert_eq!(out[0].row, Some(1));
        assert_eq!(out[0].col, Some(0));
        assert_eq!(out[0].code.as_deref(), Some("missing-module-docstring"));
        assert_eq!(out[0].severity, Some(severity::INFO));
        assert_eq!(out[1].severity, Some(severity::ERROR));
        assert_eq!(out[1].code.as_deref(), Some("undefined-variable"));
    }

    #[test]
    fn warning_maps_to_warning() {
        let json = br#"[{"type":"warning","line":2,"column":0,
            "symbol":"unused-variable","message":"Unused variable"}]"#;
        let out = parse(json, b"", 4);
        assert_eq!(out[0].severity, Some(severity::WARNING));
    }
}
