//! markuplint — A linter for all markup developers. Ported from the none-ls
//! diagnostics/markuplint builtin.
//!
//! markuplint `--format JSON` emits an array of objects with `line`, `col`,
//! `ruleId`, `severity` (`error`, `warning` or `info`) and `message`. It prints
//! a start only, so no end is reported. The builtin sets a constant
//! `source = "markuplint"` and runs against a temp file (`$FILENAME`).
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "markuplint",
	description: "A linter for all markup developers.",
	url: "https://github.com/markuplint/markuplint",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("info", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "JSON", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let attrs = Attrs {
		row: "line",
		col: "col",
		code: "ruleId",
		message: "message",
		severity: "severity",
		// markuplint reports the path per violation ("filePath"), which one run
		// over many files needs.
		file: "filePath",
		..Attrs::defaults()
	};
	let mut out = json_diag::from_json(stdout, &attrs, severity_of);
	for d in &mut out {
		d.source = Some("markuplint".to_string());
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
	fn parses_markuplint_json() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 3);
		assert_eq!(out[0].message, "Required 'alt' on '<img>'");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].end_row, None);
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[0].code.as_deref(), Some("required-attr"));
		assert_eq!(out[0].source.as_deref(), Some("markuplint"));
		assert_eq!(out[0].file.as_deref(), Some("index.html"));
	}

	#[test]
	fn reads_the_printed_level() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		let levels: Vec<_> = out.iter().map(|d| d.severity).collect();
		assert_eq!(
			levels,
			[Some(severity::ERROR), Some(severity::WARNING), Some(severity::INFO)]
		);
		let unknown = parse(br#"[{"severity":"fatal","line":1,"col":1,"message":"x"}]"#, b"", 1);
		assert_eq!(unknown[0].severity, None);
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
    {"severity":"error","line":3,"col":5,"raw":"<img src=\"a.png\">","ruleId":"required-attr","message":"Required 'alt' on '<img>'","filePath":"index.html"},
    {"severity":"warning","line":10,"col":1,"raw":"<center>","ruleId":"deprecated-element","message":"'<center>' is deprecated","filePath":"index.html"},
    {"severity":"info","line":12,"col":3,"raw":"<b>","ruleId":"use-list","message":"Use <ul> or <ol>","filePath":"index.html"}
]"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"[
    {"severity":"warning","line":10,"col":1,"raw":"<center>","ruleId":"deprecated-element","message":"'<center>' is deprecated","filePath":"index.html"}
]"#,
		stderr: b"",
		exit: 0,
	},
];
