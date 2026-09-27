//! vacuum — OpenAPI linter. Ported from the none-ls diagnostics/vacuum builtin.
//!
//! vacuum emits JSON shaped as `{ "resultSet": { "results": [ … ] } }`, where each
//! result is `{ message, ruleId, ruleSeverity, range: { start:{line,character},
//! end:{line,character} } }`. That nesting is beyond the flat `json_diag::Attrs`
//! mapper, so the navigation is hand-written over `tinyjson`. Despite the
//! LSP-style names, `vacuum report` counts lines and characters from 1 and its
//! end is exclusive (the key `info` at the start of line 2 is `2:1`–`2:5`), so the
//! range passes through unchanged.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "vacuum",
	description: "The world\u{2019}s fastest and most scalable OpenAPI linter.",
	url: "https://quobix.com/vacuum",
	severities: &[
		Level("error", severity::ERROR),
		Level("warn", severity::WARNING),
		Level("info", severity::INFO),
		Level("hint", severity::HINT),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["report", "--stdin", "--stdout"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let results = match value
		.get::<HashMap<String, JsonValue>>()
		.and_then(|m| m.get("resultSet"))
		.and_then(|rs| rs.get::<HashMap<String, JsonValue>>())
		.and_then(|m| m.get("results"))
		.and_then(|r| r.get::<Vec<JsonValue>>())
	{
		Some(r) => r,
		None => return Vec::new(),
	};
	results.iter().filter_map(diag_of).collect()
}

fn diag_of(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = value.get::<HashMap<String, JsonValue>>()?;
	let message = get_str(map, "message")?;
	let (row, col) = range_point(map, "start");
	let (end_row, end_col) = range_point(map, "end");
	Some(RawDiagnostic {
		message,
		row,
		col,
		end_row,
		end_col,
		source: Some("Vacuum".to_string()),
		code: get_str(map, "ruleId"),
		severity: get_str(map, "ruleSeverity").and_then(|s| severity::of(DESCRIPTOR.severities, &s)),
		..RawDiagnostic::default()
	})
}

/// Reads `range.<which>.line` / `range.<which>.character`.
fn range_point(map: &HashMap<String, JsonValue>, which: &str) -> (Option<u32>, Option<u32>) {
	let point = map
		.get("range")
		.and_then(|r| r.get::<HashMap<String, JsonValue>>())
		.and_then(|m| m.get(which))
		.and_then(|p| p.get::<HashMap<String, JsonValue>>());
	match point {
		Some(p) => (get_u32(p, "line"), get_u32(p, "character")),
		None => (None, None),
	}
}

fn get_str(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	map.get(key).and_then(|v| v.get::<String>()).cloned()
}

fn get_u32(map: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_nested_result() {
		let json = br#"{
            "resultSet": {
                "results": [
                    {
                        "message": "Operation must define an operationId",
                        "ruleId": "operation-operationId",
                        "ruleSeverity": "warn",
                        "range": {
                            "start": { "line": 12, "character": 4 },
                            "end": { "line": 12, "character": 20 }
                        }
                    }
                ]
            }
        }"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Operation must define an operationId");
		assert_eq!(out[0].code.as_deref(), Some("operation-operationId"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].source.as_deref(), Some("Vacuum"));
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(4));
		assert_eq!(out[0].end_row, Some(12));
		assert_eq!(out[0].end_col, Some(20));
	}

	#[test]
	fn the_recorded_report_keeps_its_one_based_range() {
		let s = &SAMPLES[0];
		let out = parse(s.stdout, s.stderr, s.exit);
		assert_eq!(out.len(), 5);
		// `info:` opens line 2 of the spec; the key spans characters 1..=4.
		assert_eq!(out[1].code.as_deref(), Some("info-description"));
		assert_eq!(
			(out[1].row, out[1].col, out[1].end_row, out[1].end_col),
			(Some(2), Some(1), Some(2), Some(5))
		);
		assert_eq!(out[1].severity, Some(severity::ERROR));
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn reads_info_and_hint() {
		let s = &SAMPLES[1];
		let levels: Vec<_> = parse(s.stdout, s.stderr, s.exit).iter().map(|d| d.severity).collect();
		assert_eq!(levels, [Some(severity::INFO), Some(severity::HINT)]);
	}

	#[test]
	fn no_results_yields_empty() {
		assert!(parse(br#"{"resultSet":{"results":[]}}"#, b"", 0).is_empty());
		assert!(parse(br#"{"resultSet":{}}"#, b"", 0).is_empty());
	}

	#[test]
	fn unknown_severity_left_unset() {
		let json = br#"{"resultSet":{"results":[
            {"message":"x","ruleId":"r","ruleSeverity":"bogus",
             "range":{"start":{"line":1,"character":1},"end":{"line":1,"character":2}}}
        ]}}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, None);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	// vacuum 0.30.6 over a ten-line OpenAPI 3.0 spec.
	crate::contract::Sample {
		stdout: br#"{
  "generated": "2026-09-27T19:23:53.802111255+03:00",
  "specInfo": {
    "type": "openapi",
    "numLines": 11,
    "version": "3.0.0",
    "versionNumeric": 3,
    "format": "oas3",
    "fileType": "yaml"
  },
  "statistics": {
    "filesizeBytes": 130,
    "specType": "openapi",
    "specFormat": "oas3",
    "version": "3.0.0",
    "paths": 1,
    "operations": 1,
    "overallScore": 69,
    "totalErrors": 2,
    "totalWarnings": 3
  },
  "resultSet": {
    "results": [
      {
        "message": "no servers defined for the specification",
        "range": {
          "start": {
            "line": 1,
            "character": 1
          },
          "end": {
            "line": 1,
            "character": 1
          }
        },
        "path": "$.servers",
        "ruleId": "oas3-api-servers",
        "ruleSeverity": "warn"
      },
      {
        "message": "`info` section must have a `description`",
        "range": {
          "start": {
            "line": 2,
            "character": 1
          },
          "end": {
            "line": 2,
            "character": 5
          }
        },
        "path": "$.info",
        "ruleId": "info-description",
        "ruleSeverity": "error"
      },
      {
        "message": "operation method `GET` at path `/pets` is missing a description or summary",
        "range": {
          "start": {
            "line": 7,
            "character": 5
          },
          "end": {
            "line": 7,
            "character": 8
          }
        },
        "path": "$.paths['/pets'].get",
        "ruleId": "operation-description",
        "ruleSeverity": "warn"
      },
      {
        "message": "the `GET` operation does not contain an `operationId`",
        "range": {
          "start": {
            "line": 7,
            "character": 5
          },
          "end": {
            "line": 7,
            "character": 8
          }
        },
        "path": "$.paths['/pets'].get",
        "ruleId": "operation-operationId",
        "ruleSeverity": "error"
      },
      {
        "message": "tags for `GET` operation are missing",
        "range": {
          "start": {
            "line": 7,
            "character": 5
          },
          "end": {
            "line": 7,
            "character": 8
          }
        },
        "path": "$.paths['/pets'].get",
        "ruleId": "operation-tags",
        "ruleSeverity": "warn"
      }
    ],
    "warningCount": 3,
    "errorCount": 2,
    "infoCount": 0,
    "hintCount": 0
  }
}
"#,
		stderr: b"",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: br#"{"resultSet":{"results":[{"message":"schema `Pet` has no example","range":{"start":{"line":14,"character":7},"end":{"line":14,"character":10}},"path":"$.components.schemas['Pet']","ruleId":"oas3-missing-example","ruleSeverity":"info"},{"message":"description duplicates the summary","range":{"start":{"line":9,"character":7},"end":{"line":9,"character":18}},"path":"$.paths['/pets'].get.description","ruleId":"description-duplication","ruleSeverity":"hint"}],"warningCount":0,"errorCount":0,"infoCount":1,"hintCount":1}}
"#,
		stderr: b"",
		exit: 0,
	},
];
