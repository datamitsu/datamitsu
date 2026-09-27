//! pmd — An extensible cross-language static code analyzer. Ported from the
//! none-ls diagnostics/pmd builtin.
//!
//! PMD emits a nested JSON report (`{files:[{violations:[…]}]}`), so it cannot
//! use the flat `json_diag` helper — we navigate the structure directly. Each
//! violation supplies begin/end positions (1-based; PMD 7's end column is past
//! the span), a `ruleset/rule` code, a description message, an
//! `externalInfoUrl`, and a numeric `priority` read as its decimal token (1 and 2
//! → error, 3 → warning, 4 → info, 5 → hint, the builtin's `max(1, priority - 1)`).

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "pmd",
	description: "An extensible cross-language static code analyzer.",
	url: "https://pmd.github.io",
	severities: &[
		Level("1", severity::ERROR),
		Level("2", severity::ERROR),
		Level("3", severity::WARNING),
		Level("4", severity::INFO),
		Level("5", severity::HINT),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "json", "--dir", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let mut out = Vec::new();
	let files = match &value {
		JsonValue::Object(m) => match m.get("files") {
			Some(JsonValue::Array(files)) => files,
			_ => return out,
		},
		_ => return out,
	};
	for file in files {
		let violations = match file {
			JsonValue::Object(m) => match m.get("violations") {
				Some(JsonValue::Array(vs)) => vs,
				_ => continue,
			},
			_ => continue,
		};
		for v in violations {
			if let Some(d) = violation_to_diag(v) {
				out.push(d);
			}
		}
	}
	out
}

fn violation_to_diag(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = get_str(map, "description")?;
	Some(RawDiagnostic {
		message,
		row: get_u32(map, "beginline"),
		col: get_u32(map, "begincolumn"),
		end_row: get_u32(map, "endline"),
		end_col: get_u32(map, "endcolumn"),
		code: code_of(map),
		url: get_str(map, "externalInfoUrl").filter(|u| !u.is_empty()),
		severity: get_u32(map, "priority").and_then(|p| severity::of(DESCRIPTOR.severities, &p.to_string())),
		..RawDiagnostic::default()
	})
}

fn code_of(map: &std::collections::HashMap<String, JsonValue>) -> Option<String> {
	let ruleset = get_str(map, "ruleset")?;
	let rule = get_str(map, "rule")?;
	Some(format!("{ruleset}/{rule}"))
}

fn get_str(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_nested_violations() {
		let out = parse(SAMPLES[0].stdout, b"", 4);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Avoid unused local variables such as 'x'.");
		assert_eq!(out[0].row, Some(10));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_row, Some(10));
		assert_eq!(out[0].end_col, Some(20));
		assert_eq!(out[0].code.as_deref(), Some("Best Practices/UnusedLocalVariable"));
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://docs.pmd-code.org/pmd-doc-7.7.0/pmd_rules_java_bestpractices.html#unusedlocalvariable")
		);
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[1].url, None);
	}

	#[test]
	fn reads_every_priority_as_its_token() {
		let levels: Vec<_> = (1..=6)
			.map(|p| {
				let json = format!(r#"{{"files":[{{"violations":[{{"priority":{p},"description":"x"}}]}}]}}"#);
				parse(json.as_bytes(), b"", 4)[0].severity
			})
			.collect();
		assert_eq!(
			levels,
			[
				Some(severity::ERROR),
				Some(severity::ERROR),
				Some(severity::WARNING),
				Some(severity::INFO),
				Some(severity::HINT),
				None
			]
		);
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 0).is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: REPORT,
		stderr: b"",
		exit: 4,
	},
	crate::contract::Sample {
		stdout: REPORT,
		stderr: b"",
		exit: 0,
	},
];

#[cfg(test)]
const REPORT: &[u8] = br#"{
  "formatVersion": 0,
  "pmdVersion": "7.7.0",
  "timestamp": "2026-09-27T10:00:00.000+00:00",
  "files": [
    {
      "filename": "Foo.java",
      "violations": [
        {
          "beginline": 10,
          "begincolumn": 5,
          "endline": 10,
          "endcolumn": 20,
          "description": "Avoid unused local variables such as 'x'.",
          "rule": "UnusedLocalVariable",
          "ruleset": "Best Practices",
          "priority": 3,
          "externalInfoUrl": "https://docs.pmd-code.org/pmd-doc-7.7.0/pmd_rules_java_bestpractices.html#unusedlocalvariable"
        },
        {
          "beginline": 1,
          "begincolumn": 1,
          "endline": 30,
          "endcolumn": 2,
          "description": "This class has too many methods, consider refactoring it.",
          "rule": "TooManyMethods",
          "ruleset": "Design",
          "priority": 1,
          "externalInfoUrl": ""
        }
      ]
    }
  ],
  "suppressedViolations": [],
  "processingErrors": [],
  "configurationErrors": []
}"#;
