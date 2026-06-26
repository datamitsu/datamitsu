//! swiftlint — A tool to enforce Swift style and conventions. Ported from the
//! none-ls diagnostics/swiftlint builtin.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "swiftlint",
	description: "A tool to enforce Swift style and conventions.",
	url: "https://github.com/realm/SwiftLint",
	operations: &[Operation {
		mode: "lint",
		args: &["--reporter", "json", "--use-stdin", "--quiet"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// SwiftLint's JSON uses `line` (default), `character`, `rule_id`, `reason`,
	// and a `severity` token ("warning"/"error").
	let attrs = Attrs {
		col: "character",
		code: "rule_id",
		message: "reason",
		severity: "severity",
		..Attrs::defaults()
	};
	json_diag::from_json(stdout, &attrs, severity_of)
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"warning" => Some(severity::WARNING),
		"error" => Some(severity::ERROR),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_swiftlint_json() {
		let json = br#"[
            {"line":12,"character":5,"rule_id":"force_cast","reason":"Force casts should be avoided.","severity":"warning","file":"/x/A.swift"},
            {"line":3,"character":1,"rule_id":"trailing_newline","reason":"Files should have a single trailing newline.","severity":"error","file":"/x/A.swift"}
        ]"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Force casts should be avoided.");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].code.as_deref(), Some("force_cast"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[1].severity, Some(severity::ERROR));
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}
}
