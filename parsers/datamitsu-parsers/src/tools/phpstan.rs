//! phpstan — PHP static analysis tool. Ported from the none-ls diagnostics/phpstan builtin.
//!
//! PHPStan's `--error-format json` output is nested:
//! `{"files": {"<path>": {"errors": N, "messages": [{message, line, ...}]}}, ...}`.
//! The builtin's `on_output` drills into `output.files[path].messages` and feeds
//! that array through the default JSON diagnostics parser. Since the parser does
//! not receive the file path, we iterate every file's `messages` array. PHPStan
//! messages carry `message`, `line`, an optional `identifier` (the rule, taken as
//! the code) and a non-diagnostic `ignorable` flag and `tip`. There is no column
//! and no level: PHPStan's "level" is the strictness of the whole analysis, not a
//! property of a finding, so no finding carries one.

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "phpstan",
	description: "PHP static analysis tool.",
	url: "https://github.com/phpstan/phpstan",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
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

	let attrs = Attrs {
		code: "identifier",
		..Attrs::defaults()
	};
	let mut out = Vec::new();

	// Navigate {"files": {"<path>": {"messages": [...]}}}.
	if let JsonValue::Object(root) = &value {
		if let Some(JsonValue::Object(files)) = root.get("files") {
			for (path, file) in files {
				if let JsonValue::Object(file_obj) = file {
					if let Some(JsonValue::Array(messages)) = file_obj.get("messages") {
						for msg in messages {
							if let Some(mut d) = json_diag::from_obj(msg, &attrs, severity_of) {
								d.file = crate::diagnostic::exact_file_field(path);
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

fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_nested_file_messages() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Undefined variable: $bar");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].code.as_deref(), Some("variable.undefined"));
		assert_eq!(out[1].message, "Method Foo::baz() not found.");
		assert_eq!(out[1].row, Some(30));
		assert_eq!(out[1].code, None);
	}

	#[test]
	fn never_sets_a_severity() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert!(out.iter().all(|d| d.severity.is_none()), "{out:?}");
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
	#[test]
	fn names_the_file_each_message_is_under() {
		let json = br#"{"files":{"src/A.php":{"errors":1,"messages":[{"message":"m","line":3}]}}}"#;
		assert_eq!(parse(json, b"", 1)[0].file.as_deref(), Some("src/A.php"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"{
    "totals": {"errors": 0, "file_errors": 2},
    "files": {
        "/app/src/Foo.php": {
            "errors": 2,
            "messages": [
                {"message": "Undefined variable: $bar", "line": 12, "ignorable": true, "identifier": "variable.undefined"},
                {"message": "Method Foo::baz() not found.", "line": 30, "ignorable": false}
            ]
        }
    },
    "errors": []
}"#,
	stderr: b"",
	exit: 1,
}];
