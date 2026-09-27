//! opentofu_validate — OpenTofu `validate` JSON output. Ported from the none-ls
//! diagnostics/opentofu_validate builtin.
//!
//! `tofu validate -json` emits (on stdout; the builtin reads stderr, so both are
//! tried) a top-level object with a `diagnostics` array. Each entry carries
//! `summary`, an optional `detail`, a `severity` token ("error"/"warning"), and
//! an optional `range` of `{ start: {line, column}, end: {line, column} }` — an
//! HCL range, 1-based with the end column past the span. The message is the summary,
//! with `" - <detail>"` appended when a detail is present (per the builtin's
//! `on_output`). The builtin's directory-keeping / filename plumbing is
//! vim-runtime state we intentionally drop.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "opentofu_validate",
	description: "OpenTofu `validate` is a subcommand of OpenTofu to validate configuration files \
        in a directory, referring only to the configuration and not accessing any remote services \
        such as remote state, provider APIs, etc.",
	url: "https://opentofu.org/docs/cli/commands/validate",
	severities: &[Level("error", severity::ERROR), Level("warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	// from_stderr=true, to_stdin=false, multiple_files=true: tofu validates the
	// directory, not a single file fed on stdin.
	operations: &[Operation {
		mode: "lint",
		args: &["validate", "-json"],
		stdin: false,
	}],
};

/// Output is read from stderr (`from_stderr = true`); fall back to stdout if a
/// runner routes it there.
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
	let obj = match &value {
		JsonValue::Object(m) => m,
		_ => return Vec::new(),
	};
	let diags = match obj.get("diagnostics") {
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
	let severity = get_str(map, "severity").as_deref().and_then(severity_of);

	let file = match map.get("range") {
		Some(JsonValue::Object(range)) => get_str(range, "filename")
			.as_deref()
			.and_then(crate::diagnostic::exact_file_field),
		_ => None,
	};
	let (row, col, end_row, end_col) = match map.get("range") {
		Some(JsonValue::Object(range)) => {
			let (row, col) = position(range.get("start"));
			let (end_row, end_col) = position(range.get("end"));
			(row, col, end_row, end_col)
		}
		_ => (None, None, None, None),
	};

	Some(RawDiagnostic {
		message,
		row,
		col,
		end_row,
		end_col,
		severity,
		source: Some("opentofu validate".to_string()),
		file,
		..RawDiagnostic::default()
	})
}

fn position(value: Option<&JsonValue>) -> (Option<u32>, Option<u32>) {
	match value {
		Some(JsonValue::Object(m)) => (get_u32(m, "line"), get_u32(m, "column")),
		_ => (None, None),
	}
}

fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
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

	const SAMPLE: &[u8] = br#"{
        "format_version": "1.0",
        "valid": false,
        "error_count": 1,
        "warning_count": 1,
        "diagnostics": [
            {
                "severity": "error",
                "summary": "Reference to undeclared input variable",
                "detail": "An input variable with the name \"foo\" has not been declared.",
                "range": {
                    "filename": "main.tf",
                    "start": { "line": 12, "column": 3, "byte": 200 },
                    "end": { "line": 12, "column": 10, "byte": 207 }
                }
            },
            {
                "severity": "warning",
                "summary": "Deprecated attribute",
                "detail": null
            }
        ]
    }"#;

	#[test]
	fn parses_error_with_range_and_detail() {
		let out = parse(b"", SAMPLE, 1);
		assert_eq!(out.len(), 2);
		let e = &out[0];
		assert_eq!(
			e.message,
			"Reference to undeclared input variable - An input variable with the name \"foo\" has not been declared."
		);
		assert_eq!(e.severity, Some(severity::ERROR));
		assert_eq!(e.row, Some(12));
		assert_eq!(e.col, Some(3));
		assert_eq!(e.end_row, Some(12));
		assert_eq!(e.end_col, Some(10));
		assert_eq!(e.source.as_deref(), Some("opentofu validate"));
	}

	#[test]
	fn warning_without_range_or_detail_uses_summary_only() {
		let out = parse(b"", SAMPLE, 1);
		let w = &out[1];
		assert_eq!(w.message, "Deprecated attribute");
		assert_eq!(w.severity, Some(severity::WARNING));
		assert_eq!(w.row, None);
		assert_eq!(w.col, None);
	}

	#[test]
	fn reads_from_stdout_when_stderr_empty() {
		let out = parse(SAMPLE, b"", 1);
		assert_eq!(out.len(), 2);
	}

	#[test]
	fn reads_a_recorded_stdout_report() {
		let out = parse(SAMPLES[0].stdout, SAMPLES[0].stderr, SAMPLES[0].exit);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!((out[0].row, out[0].col), (Some(2), Some(11)));
		assert_eq!((out[0].end_row, out[0].end_col), (Some(2), Some(18)));

		let out = parse(SAMPLES[1].stdout, SAMPLES[1].stderr, SAMPLES[1].exit);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"", b"not json", 1).is_empty());
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
	// OpenTofu v1.12.6 on `output "x" { value = var.foo }`.
	crate::contract::Sample {
		stdout: br#"{
  "format_version": "1.0",
  "valid": false,
  "error_count": 1,
  "warning_count": 0,
  "diagnostics": [
    {
      "severity": "error",
      "summary": "Reference to undeclared input variable",
      "detail": "An input variable with the name \"foo\" has not been declared. This variable can be declared with a variable \"foo\" {} block.",
      "range": {
        "filename": "main.tf",
        "start": {
          "line": 2,
          "column": 11,
          "byte": 23
        },
        "end": {
          "line": 2,
          "column": 18,
          "byte": 30
        }
      },
      "snippet": {
        "context": "output \"x\"",
        "code": "  value = var.foo",
        "start_line": 2,
        "highlight_start_offset": 10,
        "highlight_end_offset": 17,
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
        "start": { "line": 3, "column": 3, "byte": 40 },
        "end": { "line": 3, "column": 7, "byte": 44 }
      }
    }
  ]
}
"#,
		stderr: b"",
		exit: 0,
	},
];
