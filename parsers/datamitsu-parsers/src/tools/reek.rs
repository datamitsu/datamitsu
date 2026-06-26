//! reek — code smell detector for Ruby. Ported from the none-ls diagnostics/reek
//! builtin.
//!
//! reek emits a JSON array of records; each record carries a `lines` array (the
//! source lines the smell touches), a `smell_type`, a `message`, and a `source`
//! filename. The builtin expands every record into one diagnostic per entry in
//! `lines`: message is `"{smell_type}: {message}"`, severity is always warning,
//! col is 0 and end_col 1. Output is read from stderr (`from_stderr = true`).

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "reek",
	description: "Code smell detector for Ruby",
	url: "https://github.com/troessner/reek",
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
	let smell_type = match map.get("smell_type") {
		Some(JsonValue::String(s)) => s.as_str(),
		_ => return,
	};
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.as_str(),
		_ => return,
	};
	let full_message = format!("{smell_type}: {message}");

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
			message: full_message.clone(),
			row: Some(row),
			col: Some(0),
			end_col: Some(1),
			severity: Some(severity::WARNING),
			..RawDiagnostic::default()
		});
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn expands_one_diagnostic_per_line() {
		let json = br#"[
            {"context":"Dirty","lines":[3,5],"message":"has the variable name 'x'",
             "smell_type":"UncommunicativeVariableName","source":"dirty.rb"},
            {"context":"Dirty#foo","lines":[7],"message":"has approx 6 statements",
             "smell_type":"TooManyStatements","source":"dirty.rb"}
        ]"#;
		let out = parse(b"", json, 0);
		assert_eq!(out.len(), 3);
		assert_eq!(out[0].message, "UncommunicativeVariableName: has the variable name 'x'");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(0));
		assert_eq!(out[0].end_col, Some(1));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[1].row, Some(5));
		assert_eq!(out[2].message, "TooManyStatements: has approx 6 statements");
		assert_eq!(out[2].row, Some(7));
	}

	#[test]
	fn empty_array_yields_nothing() {
		assert!(parse(b"", b"[]", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"", b"not json", 0).is_empty());
	}
}
