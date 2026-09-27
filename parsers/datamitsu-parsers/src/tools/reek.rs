//! reek — code smell detector for Ruby. Ported from the none-ls diagnostics/reek
//! builtin.
//!
//! reek emits a JSON array of records; each record carries a `lines` array (the
//! source lines the smell touches), a `smell_type` (the rule), a `message`, a
//! `documentation_link` and a `source` filename. Like the builtin, every record
//! expands into one diagnostic per entry in `lines`. reek prints no level and no
//! column, so findings carry neither. Output is read from stderr
//! (`from_stderr = true`).

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "reek",
	description: "Code smell detector for Ruby",
	url: "https://github.com/troessner/reek",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "json", "--stdin-filename", "{file}"],
		stdin: true,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stderr);
	let decoded: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let records = match &decoded {
		JsonValue::Array(items) => items,
		_ => return Vec::new(),
	};

	let mut out = Vec::new();
	for record in records {
		expand_record(record, &mut out);
	}
	out
}

fn expand_record(record: &JsonValue, out: &mut Vec<RawDiagnostic>) {
	let map = match record {
		JsonValue::Object(m) => m,
		_ => return,
	};
	let (Some(smell_type), Some(message)) = (get_str(map, "smell_type"), get_str(map, "message")) else {
		return;
	};
	let url = get_str(map, "documentation_link");
	// reek names piped source "STDIN" unless --stdin-filename names it.
	let file = get_str(map, "source")
		.as_deref()
		.and_then(crate::diagnostic::file_field)
		.filter(|f| f != "STDIN");

	let lines = match map.get("lines") {
		Some(JsonValue::Array(items)) => items,
		_ => return,
	};
	for line in lines {
		let row = match line {
			JsonValue::Number(n) => match crate::numconv::json_u32(*n) {
				Some(v) => v,
				None => continue,
			},
			_ => continue,
		};
		out.push(RawDiagnostic {
			message: message.clone(),
			row: Some(row),
			code: Some(smell_type.clone()),
			url: url.clone(),
			file: file.clone(),
			..RawDiagnostic::default()
		});
	}
}

fn get_str(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn expands_one_diagnostic_per_line() {
		let out = parse(b"", SAMPLES[0].stderr, 2);
		assert_eq!(out.len(), 3);
		assert_eq!(out[0].message, "has the variable name '@x'");
		assert_eq!(out[0].code.as_deref(), Some("UncommunicativeVariableName"));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://github.com/troessner/reek/blob/v6.3.0/docs/Uncommunicative-Variable-Name.md")
		);
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[1].row, Some(5));
		assert_eq!(out[2].message, "has approx 6 statements");
		assert_eq!(out[2].code.as_deref(), Some("TooManyStatements"));
		assert_eq!(out[2].row, Some(7));
	}

	#[test]
	fn never_sets_a_severity() {
		let out = parse(b"", SAMPLES[0].stderr, 2);
		assert!(!out.is_empty());
		assert!(out.iter().all(|d| d.severity.is_none()), "{out:?}");
	}

	#[test]
	fn a_record_without_a_documentation_link_has_no_url() {
		let json = br#"[{"lines":[1],"message":"m","smell_type":"S"}]"#;
		let out = parse(b"", json, 2);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].url, None);
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"", b"[]", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"", b"not json", 0).is_empty());
	}

	#[test]
	fn each_finding_names_its_source_file() {
		let json = br#"[
            {"lines":[1],"message":"first","smell_type":"S","source":"lib/a.rb"},
            {"lines":[2],"message":"second","smell_type":"S","source":"lib/b.rb"},
            {"lines":[3],"message":"piped","smell_type":"S","source":"STDIN"}]"#;
		let out = parse(b"", json, 2);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(
			got,
			[
				("first", Some("lib/a.rb")),
				("second", Some("lib/b.rb")),
				("piped", None)
			]
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"",
	stderr: br#"[
            {"context":"Dirty","lines":[3,5],"message":"has the variable name '@x'",
             "smell_type":"UncommunicativeVariableName","source":"dirty.rb","name":"@x",
             "documentation_link":"https://github.com/troessner/reek/blob/v6.3.0/docs/Uncommunicative-Variable-Name.md"},
            {"context":"Dirty#foo","lines":[7],"message":"has approx 6 statements",
             "smell_type":"TooManyStatements","source":"dirty.rb","count":6,
             "documentation_link":"https://github.com/troessner/reek/blob/v6.3.0/docs/Too-Many-Statements.md"}
        ]"#,
	exit: 2,
}];
