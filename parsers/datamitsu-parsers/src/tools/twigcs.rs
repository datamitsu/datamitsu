//! twigcs — Runs Twigcs against Twig files. Ported from the none-ls
//! diagnostics/twigcs builtin.
//!
//! Twigcs emits `{"files":[{"file":...,"violations":[...]}],...}`. Each violation carries
//! `line`, `column`, a human `message`, and a NUMERIC `severity`: twigcs's own
//! levels 1 (info), 2 (warning) and 3 (error). Because the severity is a number
//! (not a string token) and the diagnostics are nested under each
//! `files[].violations`, this needs a bespoke navigator rather than the shared
//! `json_diag::from_json` string path.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "twigcs",
	description: "Runs Twigcs against Twig files.",
	url: "https://github.com/friendsoftwig/twigcs",
	severities: &[
		Level("1", severity::INFO),
		Level("2", severity::WARNING),
		Level("3", severity::ERROR),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	// to_temp_file=true -> no stdin, $FILENAME becomes {file}.
	operations: &[Operation {
		mode: "lint",
		args: &["--reporter", "json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	// none-ls reads only files[1]; a run over several templates lists each one.
	let files = value
		.get::<std::collections::HashMap<String, JsonValue>>()
		.and_then(|root| root.get("files"))
		.and_then(|files| files.get::<Vec<JsonValue>>());

	let mut out = Vec::new();
	for file in files.into_iter().flatten() {
		let Some(file) = file.get::<std::collections::HashMap<String, JsonValue>>() else {
			continue;
		};
		let path = match file.get("file") {
			Some(JsonValue::String(s)) => crate::diagnostic::exact_file_field(s),
			_ => None,
		};
		let Some(items) = file.get("violations").and_then(|v| v.get::<Vec<JsonValue>>()) else {
			continue;
		};
		for it in items {
			if let Some(mut d) = violation_to_diag(it) {
				d.file.clone_from(&path);
				out.push(d);
			}
		}
	}
	out
}

fn violation_to_diag(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = value.get::<std::collections::HashMap<String, JsonValue>>()?;
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	Some(RawDiagnostic {
		message,
		row: num_field(map, "line"),
		col: num_field(map, "column"),
		severity: map.get("severity").and_then(severity_of),
		..RawDiagnostic::default()
	})
}

fn num_field(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

fn severity_of(value: &JsonValue) -> Option<u8> {
	let n = match value {
		JsonValue::Number(n) => *n,
		_ => return None,
	};
	let level = crate::numconv::json_int(n)?;
	severity::of(DESCRIPTOR.severities, &level.to_string())
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_nested_violations() {
		let json = br#"{
            "failures": 1,
            "files": [
                {
                    "file": "template.twig",
                    "violations": [
                        {"line": 3, "column": 5, "severity": 3, "message": "The spread operator should be used."},
                        {"line": 7, "column": 1, "severity": 2, "message": "A print statement should start with one space."}
                    ]
                }
            ]
        }"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "The spread operator should be used.");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].row, Some(7));
	}

	#[test]
	fn empty_violations_yields_nothing() {
		let json = br#"{"files":[{"file":"a.twig","violations":[]}]}"#;
		assert!(parse(json, b"", 0).is_empty());
	}

	#[test]
	fn info_severity_maps_to_info() {
		let json = br#"{"files":[{"violations":[{"line":1,"column":1,"severity":1,"message":"note"}]}]}"#;
		let out = parse(json, b"", 1);
		assert_eq!(out[0].severity, Some(severity::INFO));
	}

	#[test]
	fn an_unlisted_level_sets_none() {
		for level in ["0", "4", "2.5", "\"3\""] {
			let json =
				format!(r#"{{"files":[{{"violations":[{{"line":1,"column":1,"severity":{level},"message":"m"}}]}}]}}"#);
			assert_eq!(parse(json.as_bytes(), b"", 1)[0].severity, None, "{level}");
		}
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 1).is_empty());
	}

	#[test]
	fn every_file_is_read_and_names_its_findings() {
		let json = br#"{"files":[
            {"file":"a.twig","violations":[{"line":1,"column":1,"severity":3,"message":"first"}]},
            {"file":"views/b.twig","violations":[{"line":2,"column":1,"severity":2,"message":"second"}]}]}"#;
		let out = parse(json, b"", 1);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(got, [("first", Some("a.twig")), ("second", Some("views/b.twig"))]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{"failures":2,"files":[{"file":"templates/base.html.twig","violations":[{"line":3,"column":5,"severity":3,"message":"There should be 1 space(s) after the opening of a variable."},{"line":7,"column":1,"severity":2,"message":"A print statement should start with one space."}]}]}"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"{"failures":0,"files":[{"file":"templates/base.html.twig","violations":[{"line":12,"column":9,"severity":1,"message":"Unused variable \"title\"."}]}]}"#,
		stderr: b"",
		exit: 0,
	},
];
