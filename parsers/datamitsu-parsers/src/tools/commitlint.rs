//! commitlint — checks commit messages against the conventional-commit format.
//! Ported from the none-ls diagnostics/commitlint builtin.
//!
//! Expects the `commitlint-format-json` formatter output:
//! `{"results":[{"errors":[{name,message,level},…],"warnings":[…]}]}`.
//! Each violation carries an integer `level` (1 = warning, 2 = error).
//! commitlint prints no position. `body-leading-blank` is about line 2 by its
//! definition, so it gets that row; the builtin also put every other `body*`
//! rule on line 3, which names a line the finding may not be on, so those have
//! none.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "commitlint",
	description: "commitlint checks if your commit messages meet the conventional commit format.",
	url: "https://commitlint.js.org",
	severities: &[Level("1", severity::WARNING), Level("2", severity::ERROR)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "commitlint-format-json"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	let mut out = Vec::new();
	// The builtin reads only results[0], but a range (`--from`/`--to`) lints one
	// commit message per result.
	let results = value
		.get::<std::collections::HashMap<String, JsonValue>>()
		.and_then(|m| m.get("results"))
		.and_then(|r| r.get::<Vec<JsonValue>>());
	for result in results.into_iter().flatten() {
		let JsonValue::Object(result) = result else {
			continue;
		};
		for key in ["errors", "warnings"] {
			if let Some(JsonValue::Array(items)) = result.get(key) {
				for it in items {
					if let Some(d) = violation_to_diagnostic(it) {
						out.push(d);
					}
				}
			}
		}
	}
	out
}

fn violation_to_diagnostic(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	let name = match map.get("name") {
		Some(JsonValue::String(s)) => Some(s.as_str()),
		_ => None,
	};

	let row = name.and_then(|n| if n == "body-leading-blank" { Some(2) } else { None });

	Some(RawDiagnostic {
		message,
		row,
		severity: map.get("level").and_then(level_severity),
		code: name.map(str::to_string),
		..RawDiagnostic::default()
	})
}

/// commitlint emits a numeric severity: 1 = warning, 2 = error.
fn level_severity(level: &JsonValue) -> Option<u8> {
	let n = match level {
		JsonValue::Number(n) => *n,
		_ => return None,
	};
	let token = crate::numconv::json_int(n)?.to_string();
	severity::of(DESCRIPTOR.severities, &token)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_errors_and_warnings() {
		let json = br#"{"results":[{
            "errors":[
                {"level":2,"name":"type-empty","message":"type may not be empty"},
                {"level":2,"name":"body-leading-blank","message":"body must have leading blank line"}
            ],
            "warnings":[
                {"level":1,"name":"body-max-line-length","message":"body line too long"}
            ]
        }]}"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 3);

		assert_eq!(out[0].message, "type may not be empty");
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].code.as_deref(), Some("type-empty"));
		assert_eq!(out[0].row, None);

		// body-leading-blank -> line 2
		assert_eq!(out[1].row, Some(2));
		// No other rule says which line it is about.
		assert_eq!(out[2].row, None);
		assert_eq!(out[2].severity, Some(severity::WARNING));
	}

	#[test]
	fn a_missing_or_unknown_level_sets_none() {
		let json = br#"{"results":[{"errors":[
                {"name":"type-empty","message":"no level"},
                {"level":0,"name":"type-case","message":"disabled level"},
                {"level":2.5,"name":"scope-empty","message":"not an integer"}
            ]}]}"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 3);
		assert!(out.iter().all(|d| d.severity.is_none()));
	}

	#[test]
	fn no_results_yields_nothing() {
		assert!(parse(br#"{"results":[]}"#, b"", 0).is_empty());
		assert!(parse(b"not json", b"", 0).is_empty());
	}

	#[test]
	fn every_linted_message_is_read() {
		let json = br#"{"results":[
            {"errors":[{"level":2,"name":"type-empty","message":"first"}],"warnings":[]},
            {"errors":[{"level":2,"name":"subject-empty","message":"second"}],"warnings":[]}]}"#;
		let messages: Vec<_> = parse(json, b"", 1).into_iter().map(|d| d.message).collect();
		assert_eq!(messages, ["first", "second"]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: br#"{"valid":false,"errorCount":2,"warningCount":1,"results":[{"valid":false,"errors":[{"level":2,"valid":false,"name":"type-empty","message":"type may not be empty"},{"level":2,"valid":false,"name":"subject-empty","message":"subject may not be empty"}],"warnings":[{"level":1,"valid":false,"name":"body-leading-blank","message":"body must have leading blank line"}],"input":"foo bar\nbody text"}]}"#,
	stderr: b"",
	exit: 1,
}];
