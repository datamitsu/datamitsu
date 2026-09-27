//! pylint — Python static code analysis tool. Ported from the none-ls
//! diagnostics/pylint builtin.
//!
//! pylint's `column` and `endColumn` count from 0 (its `line`/`endLine` from 1),
//! so both columns are shifted to 1-based; `endColumn` is already exclusive.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "pylint",
	description: "Pylint is a Python static code analysis tool which looks for programming errors, helps enforcing a coding standard, sniffs for code smells and offers simple refactoring suggestions.",
	url: "https://github.com/PyCQA/pylint",
	severities: &[
		Level("fatal", severity::ERROR),
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		// none-ls maps convention/refactor to "information"; pylint's "info" maps there too.
		Level("convention", severity::INFO),
		Level("refactor", severity::INFO),
		Level("info", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
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
	let mut out = json_diag::from_json(stdout, &attrs, severity_of);
	for d in &mut out {
		d.col = d.col.map(|c| c + 1);
		d.end_col = d.end_col.map(|c| c + 1);
	}
	out
}

fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_pylint_json() {
		let out = parse(SAMPLES[0].stdout, b"", 18);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Missing module docstring");
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_row, None);
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[0].code.as_deref(), Some("missing-module-docstring"));
		assert_eq!(out[0].severity, Some(severity::INFO));
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[1].code.as_deref(), Some("undefined-variable"));
	}

	#[test]
	fn zero_based_columns_become_one_based_with_an_exclusive_end() {
		let out = parse(SAMPLES[0].stdout, b"", 18);
		assert_eq!((out[1].row, out[1].col), (Some(3), Some(5)));
		assert_eq!((out[1].end_row, out[1].end_col), (Some(3), Some(6)));
	}

	#[test]
	fn reads_every_printed_level() {
		let out = parse(SAMPLES[1].stdout, b"", 13);
		let levels: Vec<_> = out.iter().map(|d| d.severity).collect();
		assert_eq!(
			levels,
			[Some(severity::WARNING), Some(severity::INFO), Some(severity::ERROR)]
		);
	}

	#[test]
	fn an_unknown_type_has_no_level() {
		let json = br#"[{"type":"information","line":2,"column":0,"symbol":"s","message":"m"}]"#;
		assert_eq!(parse(json, b"", 0)[0].severity, None);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"[
            {"type":"convention","module":"foo","obj":"","line":1,"column":0,"endLine":null,"endColumn":null,
             "path":"foo.py","symbol":"missing-module-docstring",
             "message":"Missing module docstring","message-id":"C0114"},
            {"type":"error","module":"foo","obj":"","line":3,"column":4,"endLine":3,"endColumn":5,
             "path":"foo.py","symbol":"undefined-variable",
             "message":"Undefined variable 'x'","message-id":"E0602"}
        ]"#,
		stderr: b"",
		exit: 18,
	},
	crate::contract::Sample {
		stdout: br#"[
            {"type":"warning","module":"foo","obj":"f","line":2,"column":4,"endLine":2,"endColumn":10,
             "path":"foo.py","symbol":"unused-variable","message":"Unused variable 'unused'","message-id":"W0612"},
            {"type":"refactor","module":"foo","obj":"g","line":5,"column":0,"endLine":5,"endColumn":5,
             "path":"foo.py","symbol":"too-many-return-statements",
             "message":"Too many return statements (7/6)","message-id":"R0911"},
            {"type":"fatal","module":"foo","obj":"","line":1,"column":0,"endLine":null,"endColumn":null,
             "path":"foo.py","symbol":"astroid-error","message":"foo.py: Fatal error while checking","message-id":"F0002"}
        ]"#,
		stderr: b"",
		exit: 13,
	},
];
