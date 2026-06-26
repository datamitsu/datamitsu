//! phpstan — PHP static analysis tool. Ported from the none-ls diagnostics/phpstan builtin.
//!
//! PHPStan's `--error-format json` output is nested:
//! `{"files": {"<path>": {"errors": N, "messages": [{message, line, ...}]}}, ...}`.
//! The builtin's `on_output` drills into `output.files[path].messages` and feeds
//! that array through the default JSON diagnostics parser. Since the parser does
//! not receive the file path, we iterate every file's `messages` array. PHPStan
//! messages carry only `message` and `line` (plus a non-diagnostic `ignorable`
//! flag and optional `identifier`); there is no column, level, or rule field in
//! this format, so only message+row populate — matching `from_json({})` defaults.

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "phpstan",
	description: "PHP static analysis tool.",
	url: "https://github.com/phpstan/phpstan",
	operations: &[Operation {
		mode: "lint",
		args: &["analyze", "--error-format", "json", "--no-progress", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	let attrs = Attrs::defaults();
	let mut out = Vec::new();

	// Navigate {"files": {"<path>": {"messages": [...]}}}.
	if let JsonValue::Object(root) = &value {
		if let Some(JsonValue::Object(files)) = root.get("files") {
			for file in files.values() {
				if let JsonValue::Object(file_obj) = file {
					if let Some(JsonValue::Array(messages)) = file_obj.get("messages") {
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

/// PHPStan's JSON format emits no severity token, so this is never consulted in
/// practice; provided to satisfy the `from_obj` signature.
fn severity_of(_level: &str) -> Option<u8> {
	None
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_nested_file_messages() {
		let json = br#"{
            "totals": {"errors": 0, "file_errors": 2},
            "files": {
                "/app/src/Foo.php": {
                    "errors": 2,
                    "messages": [
                        {"message": "Undefined variable: $bar", "line": 12, "ignorable": true},
                        {"message": "Method foo() not found.", "line": 30, "ignorable": false}
                    ]
                }
            },
            "errors": []
        }"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Undefined variable: $bar");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].severity, None);
		assert_eq!(out[1].message, "Method foo() not found.");
		assert_eq!(out[1].row, Some(30));
	}

	#[test]
	fn no_files_yields_nothing() {
		let json = br#"{"totals":{"errors":0,"file_errors":0},"files":{},"errors":[]}"#;
		assert!(parse(json, b"", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 1).is_empty());
	}
}
