//! terraform_validate — Terraform validate subcommand validates configuration
//! files in a directory. Ported from the none-ls diagnostics/terraform_validate
//! builtin.
//!
//! `terraform validate -json` emits a JSON object of the shape:
//! `{ "diagnostics": [ { "severity", "summary", "detail",
//!   "range": { "filename", "start": {"line","column"}, "end": {"line","column"} } } ] }`.
//! The builtin reads it from stderr; terraform prints it on stdout, so either
//! stream is accepted. The message is `summary`, with ` - <detail>` appended
//! when `detail` is present. Row/col come from `range.start`, end_row/end_col
//! from `range.end`: HCL positions are 1-based and the end is exclusive.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "terraform_validate",
	description: "Terraform validate is is a subcommand of terraform to validate configuration files in a directory",
	url: "https://github.com/hashicorp/terraform",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["validate", "-json"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let primary = parse_bytes(stderr);
	if primary.is_empty() {
		parse_bytes(stdout)
	} else {
		primary
	}
}

fn parse_bytes(bytes: &[u8]) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(bytes);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let map = match &value {
		JsonValue::Object(m) => m,
		_ => return Vec::new(),
	};
	let diags = match map.get("diagnostics") {
		Some(JsonValue::Array(items)) => items,
		_ => return Vec::new(),
	};
	diags.iter().filter_map(from_diag).collect()
}

fn from_diag(value: &JsonValue) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let summary = get_str(map, "summary")?;
	let message = match get_str(map, "detail") {
		Some(detail) => format!("{summary} - {detail}"),
		None => summary,
	};
	let severity = get_str(map, "severity").and_then(|s| severity::of(DESCRIPTOR.severities, &s));

	let mut diag = RawDiagnostic {
		message,
		source: Some("terraform validate".to_string()),
		severity,
		..RawDiagnostic::default()
	};

	if let Some(JsonValue::Object(range)) = map.get("range") {
		diag.file = get_str(range, "filename")
			.as_deref()
			.and_then(crate::diagnostic::file_field);
		if let Some(JsonValue::Object(start)) = range.get("start") {
			diag.row = get_u32(start, "line");
			diag.col = get_u32(start, "column");
		}
		if let Some(JsonValue::Object(end)) = range.get("end") {
			diag.end_row = get_u32(end, "line");
			diag.end_col = get_u32(end, "column");
		}
	}

	Some(diag)
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
	fn parses_error_with_range_and_detail() {
		let stderr = br#"{
            "format_version": "1.0",
            "valid": false,
            "error_count": 1,
            "diagnostics": [
                {
                    "severity": "error",
                    "summary": "Unsupported argument",
                    "detail": "An argument named \"foo\" is not expected here.",
                    "range": {
                        "filename": "main.tf",
                        "start": { "line": 3, "column": 5, "byte": 40 },
                        "end": { "line": 3, "column": 8, "byte": 43 }
                    }
                }
            ]
        }"#;
		let out = parse(b"", stderr, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(
			out[0].message,
			"Unsupported argument - An argument named \"foo\" is not expected here."
		);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("terraform validate"));
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_row, Some(3));
		assert_eq!(out[0].end_col, Some(8));
	}

	#[test]
	fn parses_warning_without_detail_or_range() {
		let stderr = br#"{
            "diagnostics": [
                { "severity": "warning", "summary": "Deprecated feature" }
            ]
        }"#;
		let out = parse(b"", stderr, 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Deprecated feature");
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].row, None);
		assert_eq!(out[0].col, None);
	}

	#[test]
	fn reads_the_report_from_stdout() {
		let out = parse(SAMPLES[0].stdout, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!((out[0].row, out[0].col, out[0].end_col), (Some(3), Some(3), Some(6)));
	}

	#[test]
	fn an_unlisted_severity_sets_no_level() {
		let out = parse(b"", br#"{"diagnostics":[{"severity":"fatal","summary":"x"}]}"#, 1);
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn valid_output_yields_nothing() {
		let stderr = br#"{"valid": true, "diagnostics": []}"#;
		assert!(parse(b"", stderr, 0).is_empty());
	}
	#[test]
	fn names_the_file_of_each_diagnostic() {
		let json = br#"{"valid":false,"diagnostics":[{"severity":"error","summary":"s","range":{"filename":"mod/main.tf","start":{"line":1,"column":1},"end":{"line":1,"column":2}}}]}"#;
		assert_eq!(parse(b"", json, 1)[0].file.as_deref(), Some("mod/main.tf"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{
  "format_version": "1.0",
  "valid": false,
  "error_count": 1,
  "warning_count": 0,
  "diagnostics": [
    {
      "severity": "error",
      "summary": "Unsupported argument",
      "detail": "An argument named \"foo\" is not expected here.",
      "range": {
        "filename": "main.tf",
        "start": {
          "line": 3,
          "column": 3,
          "byte": 33
        },
        "end": {
          "line": 3,
          "column": 6,
          "byte": 36
        }
      },
      "snippet": {
        "context": "variable \"x\"",
        "code": "  foo  = 1",
        "start_line": 3,
        "highlight_start_offset": 2,
        "highlight_end_offset": 5,
        "values": []
      }
    }
  ]
}
"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"{
  "format_version": "1.0",
  "valid": true,
  "error_count": 0,
  "warning_count": 1,
  "diagnostics": [
    {
      "severity": "warning",
      "summary": "Deprecated attribute",
      "detail": "The attribute \"name\" is deprecated.",
      "range": {
        "filename": "main.tf",
        "start": {
          "line": 7,
          "column": 10,
          "byte": 101
        },
        "end": {
          "line": 7,
          "column": 21,
          "byte": 112
        }
      }
    }
  ]
}
"#,
		stderr: b"",
		exit: 0,
	},
];
