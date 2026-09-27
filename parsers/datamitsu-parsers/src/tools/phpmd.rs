//! phpmd — PHP Mess Detector.
//!
//! Ported from the none-ls `diagnostics/phpmd` builtin (the JSON class). It runs
//! `phpmd {file} json` and emits a nested object
//! `{"files":[{"violations":[…]}]}`. Unlike the flat JSON tools, the diagnostics
//! live under `files[0].violations`, so this parser navigates there itself and
//! reuses the shared field mapping. Each violation maps `description → message`,
//! `beginLine → row`, `endLine → end_row`, `rule → code`, `externalInfoUrl → url`,
//! and a numeric `priority` (1–5) → severity (1,2 → error/warning; 3 →
//! information; 4,5 → hint).
//!
//! The builtin allows exit code ≤ 3 (phpmd uses the exit code to report whether
//! violations were found), and on a non-JSON / errored run produces a single
//! synthetic "cannot analyze/parse" diagnostic without a level.

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "phpmd",
	description: "Runs PHP Mess Detector against PHP files.",
	url: "https://github.com/phpmd/phpmd/",
	severities: &[
		Level("1", severity::ERROR),
		Level("2", severity::WARNING),
		Level("3", severity::INFO),
		Level("4", severity::HINT),
		Level("5", severity::HINT),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["{file}", "json"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => {
			return vec![RawDiagnostic {
				message: "phpmd error: cannot parse output as JSON.".to_string(),
				..RawDiagnostic::default()
			}];
		}
	};

	// phpmd nests violations under files[0].violations.
	let violations = value
		.get::<std::collections::HashMap<String, JsonValue>>()
		.and_then(|root| root.get("files"))
		.and_then(|f| f.get::<Vec<JsonValue>>())
		.and_then(|files| files.first())
		.and_then(|f0| f0.get::<std::collections::HashMap<String, JsonValue>>())
		.and_then(|f0| f0.get("violations"))
		.and_then(|v| v.get::<Vec<JsonValue>>());

	let violations = match violations {
		Some(v) => v,
		None => return Vec::new(),
	};

	let attrs = Attrs {
		message: "description",
		row: "beginLine",
		end_row: "endLine",
		code: "rule",
		// priority is numeric; handled separately below.
		severity: "priority",
		..Attrs::defaults()
	};

	violations
		.iter()
		.filter_map(|v| {
			let mut d = json_diag::from_obj(v, &attrs, |_| None)?;
			let map = v.get::<std::collections::HashMap<String, JsonValue>>()?;
			d.severity = priority_severity(map.get("priority"));
			d.url = match map.get("externalInfoUrl") {
				Some(JsonValue::String(url)) if !url.is_empty() => Some(url.clone()),
				_ => None,
			};
			Some(d)
		})
		.collect()
}

/// phpmd's numeric `priority` (1 = most severe … 5 = least), read as its decimal
/// token.
fn priority_severity(priority: Option<&JsonValue>) -> Option<u8> {
	let token = match priority? {
		JsonValue::Number(n) => crate::numconv::json_int(*n)?.to_string(),
		JsonValue::String(s) => s.trim().to_string(),
		_ => return None,
	};
	severity::of(DESCRIPTOR.severities, &token)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_nested_violations_with_priority_mapping() {
		let out = parse(SAMPLES[0].stdout, b"", 2);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "Avoid unused local variables such as '$x'.");
		assert_eq!(out[0].row, Some(10));
		assert_eq!(out[0].end_row, Some(12));
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].code.as_deref(), Some("UnusedLocalVariable"));
		assert_eq!(out[0].severity, Some(severity::INFO));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://phpmd.org/rules/unusedcode.html#unusedlocalvariable")
		);

		assert_eq!(out[1].row, Some(20));
		assert_eq!(out[1].code.as_deref(), Some("CyclomaticComplexity"));
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[1].url, None);
	}

	#[test]
	fn reads_every_priority_as_its_token() {
		let levels: Vec<_> = ["1", "2", "3", "4", "5", "6"]
			.iter()
			.map(|p| priority_severity(Some(&JsonValue::Number(p.parse().unwrap()))))
			.collect();
		assert_eq!(
			levels,
			[
				Some(severity::ERROR),
				Some(severity::WARNING),
				Some(severity::INFO),
				Some(severity::HINT),
				Some(severity::HINT),
				None
			]
		);
		assert_eq!(
			priority_severity(Some(&JsonValue::String(" 2 ".into()))),
			Some(severity::WARNING)
		);
		assert_eq!(priority_severity(None), None);
	}

	#[test]
	fn no_violations_yields_nothing() {
		let out = parse(br#"{"files":[]}"#, b"", 0);
		assert!(out.is_empty());
	}

	#[test]
	fn invalid_json_yields_synthetic_diagnostic_without_a_level() {
		let out = parse(SAMPLES[1].stdout, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "phpmd error: cannot parse output as JSON.");
		assert_eq!(out[0].severity, None);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{
  "version": "@project.version@",
  "package": "phpmd",
  "timestamp": "2026-09-27T10:00:00+00:00",
  "files": [
    {
      "file": "/src/Foo.php",
      "violations": [
        {
          "beginLine": 10,
          "endLine": 12,
          "package": null,
          "function": null,
          "class": "Foo",
          "method": "bar",
          "description": "Avoid unused local variables such as '$x'.",
          "rule": "UnusedLocalVariable",
          "ruleSet": "Unused Code Rules",
          "externalInfoUrl": "https://phpmd.org/rules/unusedcode.html#unusedlocalvariable",
          "priority": 3
        },
        {
          "beginLine": 20,
          "endLine": 20,
          "rule": "CyclomaticComplexity",
          "ruleSet": "Code Size Rules",
          "externalInfoUrl": "",
          "priority": 1,
          "description": "The method bar() has a Cyclomatic Complexity of 11."
        }
      ]
    }
  ]
}"#,
		stderr: b"",
		exit: 2,
	},
	crate::contract::Sample {
		stdout: b"The file \"src/Missing.php\" does not exist.\n",
		stderr: b"",
		exit: 1,
	},
];
