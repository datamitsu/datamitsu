//! mdl — a tool to check Markdown files and flag style issues.
//! Ported from the none-ls diagnostics/mdl builtin.
//!
//! mdl `--json` emits an array of objects: `{ filename, line, rule, aliases,
//! description, docs }`. The parser maps row=line, code=rule,
//! message=description and url=docs. The JSON carries no level, so no finding
//! has one, and there is no column or end position.
use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::json_diag::{self, Attrs};
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "mdl",
	description: "A tool to check Markdown files and flag style issues.",
	url: "https://github.com/markdownlint/markdownlint",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--json"],
		stdin: true,
	}],
};

const ATTRS: Attrs = Attrs {
	row: "line",
	code: "rule",
	message: "description",
	file: "filename",
	..Attrs::defaults()
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	json_diag::extract_lenient(stdout, |value| match value {
		JsonValue::Array(items) => items.iter().filter_map(from_result).collect(),
		_ => Vec::new(),
	})
}

fn from_result(value: &JsonValue) -> Option<RawDiagnostic> {
	let mut d = json_diag::from_obj(value, &ATTRS, |level| severity::of(DESCRIPTOR.severities, level))?;
	// mdl names what it read from stdin "(stdin)".
	if d.file.as_deref() == Some("(stdin)") {
		d.file = None;
	}
	if let JsonValue::Object(m) = value {
		d.url = match m.get("docs") {
			Some(JsonValue::String(url)) if !url.is_empty() => Some(url.clone()),
			_ => None,
		};
	}
	Some(d)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_mdl_json() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Line length");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].code.as_deref(), Some("MD013"));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://github.com/markdownlint/markdownlint/blob/main/docs/RULES.md#md013---line-length")
		);
		assert_eq!(out[0].col, None);
		assert_eq!(out[1].row, Some(10));
		assert_eq!(out[1].code.as_deref(), Some("MD009"));
		assert_eq!(out[1].url, None);
	}

	#[test]
	fn never_sets_a_severity() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert!(out.iter().all(|d| d.severity.is_none()), "{out:?}");
	}

	#[test]
	fn empty_output_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}

	#[test]
	fn each_finding_names_its_file_but_not_the_stdin_placeholder() {
		let json = br#"[
            {"filename":"docs/a.md","line":1,"rule":"MD001","description":"first"},
            {"filename":"b.md","line":2,"rule":"MD002","description":"second"}]"#;
		let out = parse(json, b"", 1);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(got, [("first", Some("docs/a.md")), ("second", Some("b.md"))]);
		assert!(parse(SAMPLES[0].stdout, b"", 1).iter().all(|d| d.file.is_none()));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"[
    {"filename":"(stdin)","line":3,"rule":"MD013","aliases":["line-length"],"description":"Line length","docs":"https://github.com/markdownlint/markdownlint/blob/main/docs/RULES.md#md013---line-length"},
    {"filename":"(stdin)","line":10,"rule":"MD009","aliases":["no-trailing-spaces"],"description":"Trailing spaces"}
]"#,
	stderr: b"",
	exit: 1,
}];
