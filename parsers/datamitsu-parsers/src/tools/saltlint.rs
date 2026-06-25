//! saltlint — checks for best practices in SaltStack. Ported from the none-ls
//! diagnostics/saltlint builtin.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "saltlint",
    description: "A command-line utility that checks for best practices in SaltStack.",
    url: "https://github.com/warpnet/salt-lint",
    operations: &[Operation {
        mode: "lint",
        args: &["--nocolor", "--json", "{file}"],
        stdin: true,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
    let attrs = Attrs {
        row: "linenumber",
        code: "id",
        message: "message",
        severity: "severity",
        ..Attrs::defaults()
    };
    json_diag::from_json(stdout, &attrs, severity_of)
}

fn severity_of(level: &str) -> Option<u8> {
    match level {
        "HIGH" => Some(severity::ERROR),
        "LOW" => Some(severity::WARNING),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_saltlint_json() {
        let json = br#"[
            {"id":"209","message":"Cannot find file","linenumber":3,"severity":"HIGH","filename":"foo.sls"},
            {"id":"207","message":"File modes should always be encapsulated in quotation marks","linenumber":7,"severity":"LOW","filename":"foo.sls"}
        ]"#;
        let out = parse(json, b"", 1);
        assert_eq!(out.len(), 2);
        assert_eq!(out[0].message, "Cannot find file");
        assert_eq!(out[0].row, Some(3));
        assert_eq!(out[0].code.as_deref(), Some("209"));
        assert_eq!(out[0].severity, Some(severity::ERROR));
        assert_eq!(out[1].severity, Some(severity::WARNING));
        assert_eq!(out[1].code.as_deref(), Some("207"));
    }

    #[test]
    fn unknown_severity_is_none() {
        let json = br#"[{"id":"1","message":"x","linenumber":1,"severity":"INFO"}]"#;
        let out = parse(json, b"", 1);
        assert_eq!(out[0].severity, None);
    }
}
