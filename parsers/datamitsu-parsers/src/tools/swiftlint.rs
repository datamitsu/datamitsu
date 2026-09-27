//! swiftlint — A tool to enforce Swift style and conventions. Ported from the
//! none-ls diagnostics/swiftlint builtin.
//!
//! The JSON reporter capitalizes the severity (`"Warning"`, `"Error"`); `line`
//! and `character` are 1-based, and `character` is null for a whole-line
//! violation.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "swiftlint",
	description: "A tool to enforce Swift style and conventions.",
	url: "https://github.com/realm/SwiftLint",
	severities: &[Level("Error", severity::ERROR), Level("Warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--reporter", "json", "--use-stdin", "--quiet"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// SwiftLint's JSON uses `line` (default), `character`, `rule_id`, `reason`,
	// and a `severity` token.
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
	severity::of(DESCRIPTOR.severities, level)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_swiftlint_json() {
		let json = br#"[
            {"line":12,"character":5,"rule_id":"force_cast","reason":"Force casts should be avoided.","severity":"Warning","type":"Force Cast","file":"/x/A.swift"},
            {"line":3,"character":null,"rule_id":"trailing_newline","reason":"Files should have a single trailing newline.","severity":"Error","type":"Trailing Newline","file":"/x/A.swift"}
        ]"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Force casts should be avoided.");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].code.as_deref(), Some("force_cast"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[1].col, None);
	}

	#[test]
	fn an_unlisted_severity_sets_no_level() {
		let json = br#"[{"line":1,"character":1,"rule_id":"r","reason":"x","severity":"warning"}]"#;
		assert_eq!(parse(json, b"", 0)[0].severity, None);
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"[
  {
    "character" : 5,
    "file" : "/work/swiftlint/A.swift",
    "line" : 12,
    "reason" : "Force casts should be avoided",
    "rule_id" : "force_cast",
    "severity" : "Error",
    "type" : "Force Cast"
  },
  {
    "character" : null,
    "file" : "/work/swiftlint/A.swift",
    "line" : 3,
    "reason" : "Files should have a single trailing newline",
    "rule_id" : "trailing_newline",
    "severity" : "Warning",
    "type" : "Trailing Newline"
  }
]
"#,
		stderr: b"",
		exit: 2,
	},
	crate::contract::Sample {
		stdout: br#"[
  {
    "character" : 1,
    "file" : "/work/swiftlint/B.swift",
    "line" : 7,
    "reason" : "Line should be 120 characters or less; currently it has 131 characters",
    "rule_id" : "line_length",
    "severity" : "Warning",
    "type" : "Line Length"
  }
]
"#,
		stderr: b"",
		exit: 0,
	},
];
