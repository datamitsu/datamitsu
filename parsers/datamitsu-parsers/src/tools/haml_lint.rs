//! haml_lint — tool for writing clean and consistent HAML. Ported from the
//! none-ls diagnostics/haml_lint builtin.
//!
//! haml-lint's JSON reporter emits `{ "files": [ { "path", "offenses": [ … ] } ] }`,
//! where each offense is `{ message, location: { line }, linter_name, severity }`.
//! The builtin walks `files[1].offenses` and maps message←message, line←location.line,
//! ruleId←linter_name, level←severity. There is no column. Severity tokens are
//! "warning" and "error".

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "haml_lint",
	description: "Tool for writing clean and consistent HAML.",
	url: "https://github.com/sds/haml-lint",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	// to_stdin=true + to_temp_file=true: stdin is piped but the tool reads a temp
	// file path ($FILENAME), so args carry {file} and stdin stays true.
	operations: &[Operation {
		mode: "lint",
		args: &["--reporter", "json", "{file}"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr=true: diagnostics arrive on stderr; fall back to stdout.
	let bytes = if stderr.is_empty() { stdout } else { stderr };
	let text = String::from_utf8_lossy(bytes);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	let mut out = Vec::new();
	let Some(files) = value
		.get::<std::collections::HashMap<String, JsonValue>>()
		.and_then(|m| m.get("files"))
		.and_then(|f| f.get::<Vec<JsonValue>>())
	else {
		return out;
	};

	for file in files {
		let Some(file) = file.get::<std::collections::HashMap<String, JsonValue>>() else {
			continue;
		};
		let Some(offenses) = file.get("offenses").and_then(|o| o.get::<Vec<JsonValue>>()) else {
			continue;
		};
		let path = file
			.get("path")
			.and_then(|p| p.get::<String>())
			.and_then(|p| crate::diagnostic::exact_file_field(p));
		for offense in offenses {
			if let Some(mut d) = from_offense(offense) {
				d.file.clone_from(&path);
				out.push(d);
			}
		}
	}
	out
}

fn from_offense(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = value.get::<std::collections::HashMap<String, JsonValue>>()?;
	// With --auto-correct haml-lint also lists what it corrected.
	if matches!(map.get("corrected"), Some(JsonValue::Boolean(true))) {
		return None;
	}
	let message = map.get("message").and_then(|m| m.get::<String>())?.clone();
	let row = map
		.get("location")
		.and_then(|l| l.get::<std::collections::HashMap<String, JsonValue>>())
		.and_then(|m| m.get("line"))
		.and_then(json_u32);
	let code = map.get("linter_name").and_then(|c| c.get::<String>()).cloned();
	let severity = map
		.get("severity")
		.and_then(|s| s.get::<String>())
		.and_then(|s| severity_of(s));
	Some(RawDiagnostic {
		message,
		row,
		code,
		severity,
		..RawDiagnostic::default()
	})
}

fn json_u32(v: &JsonValue) -> Option<u32> {
	match v {
		JsonValue::Number(n) => crate::numconv::json_u32(*n),
		JsonValue::String(s) => s.trim().parse().ok(),
		_ => None,
	}
}

fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const REPORT: &[u8] = br#"{
            "files": [
                {
                    "path": "app/views/foo.haml",
                    "offenses": [
                        {
                            "severity": "warning",
                            "message": "Line is too long. [120/80]",
                            "location": { "line": 12 },
                            "linter_name": "LineLength"
                        },
                        {
                            "severity": "error",
                            "message": "Syntax error",
                            "location": { "line": 1 },
                            "linter_name": "Syntax"
                        }
                    ]
                }
            ],
            "summary": { "offense_count": 2 }
        }"#;

	#[test]
	fn parses_offenses() {
		let out = parse(REPORT, b"", 0);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Line is too long. [120/80]");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].code.as_deref(), Some("LineLength"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].col, None);
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[1].code.as_deref(), Some("Syntax"));
	}

	#[test]
	fn a_level_haml_lint_does_not_have_is_left_unset() {
		let json =
			br#"{"files":[{"offenses":[{"severity":"fatal","message":"m","location":{"line":1},"linter_name":"X"}]}]}"#;
		assert_eq!(parse(json, b"", 1)[0].severity, None);
	}

	#[test]
	fn empty_files_yields_nothing() {
		let out = parse(br#"{"files":[],"summary":{"offense_count":0}}"#, b"", 0);
		assert!(out.is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 0).is_empty());
	}

	#[test]
	fn each_finding_names_its_file() {
		let json = br#"{"files":[
            {"path":"a.haml","offenses":[{"severity":"warning","message":"first","location":{"line":1}}]},
            {"path":"views/b.haml","offenses":[{"severity":"error","message":"second","location":{"line":2}}]}]}"#;
		let out = parse(json, b"", 65);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(got, [("first", Some("a.haml")), ("second", Some("views/b.haml"))]);
	}

	#[test]
	fn a_corrected_offense_is_not_a_finding() {
		let json = br#"{"files":[{"path":"a.haml","offenses":[
            {"severity":"warning","message":"fixed","corrected":true,"location":{"line":1}},
            {"severity":"warning","message":"left","corrected":false,"location":{"line":2}}]}]}"#;
		let out = parse(json, b"", 65);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "left");
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: tests::REPORT,
		stderr: b"",
		exit: 65,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: tests::REPORT,
		exit: 65,
	},
];
