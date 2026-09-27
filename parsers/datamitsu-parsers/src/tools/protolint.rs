//! protolint — a pluggable linter and fixer for Protocol Buffer style.
//! Ported from the none-ls diagnostics/protolint builtin.
//!
//! protolint runs with `--reporter json` and emits a JSON object `{"lints":[…]}`
//! (read `from_stderr`). Each lint carries `message`, `line`, `column`, a `rule`
//! (used as `code`) and the rule's configured `severity` (`error`, `warning` or
//! `note`). The builtin slices from the first `{` and, when no `{` is found or
//! JSON decoding fails, reports the whole output as one generic issue, which
//! carries neither a level nor a position. The `FILE_NAMES_LOWER_SNAKE_CASE` rule
//! is skipped — it is a false positive caused by linting via a temp file.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "protolint",
	description: "A pluggable linter and fixer to enforce Protocol Buffer style and conventions.",
	url: "https://github.com/yoheimuta/protolint",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("note", severity::INFO),
	],
	column_unit: "utf-32",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		// to_temp_file: protolint does not accept stdin, so it lints the file path.
		args: &["--reporter", "json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true: protolint writes its report to stderr.
	let stderr = String::from_utf8_lossy(stderr);
	let stdout = String::from_utf8_lossy(stdout);
	let output = if stderr.trim().is_empty() {
		stdout.as_ref()
	} else {
		stderr.as_ref()
	};

	if output.trim().is_empty() {
		return Vec::new();
	}

	// Slice from the first `{`; without one, nothing parseable — generic issue.
	let Some(json_index) = output.find('{') else {
		return vec![generic_issue(output)];
	};
	let maybe_json = &output[json_index..];

	let decoded: JsonValue = match maybe_json.parse() {
		Ok(v) => v,
		Err(_) => return vec![generic_issue(output)],
	};

	let lints = match &decoded {
		JsonValue::Object(map) => match map.get("lints") {
			Some(JsonValue::Array(items)) => items,
			// decoded but no lints array -> nothing to report
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};

	lints.iter().filter_map(from_lint).collect()
}

fn from_lint(lint: &JsonValue) -> Option<RawDiagnostic> {
	let map = match lint {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let rule = get_str(map, "rule");
	// Skip the temp-file false positive (see module docs).
	if rule.as_deref() == Some("FILE_NAMES_LOWER_SNAKE_CASE") {
		return None;
	}
	let message = get_str(map, "message")?;
	Some(RawDiagnostic {
		message,
		row: get_u32(map, "line"),
		col: get_u32(map, "column"),
		code: rule,
		severity: get_str(map, "severity").and_then(|s| severity::of(DESCRIPTOR.severities, &s)),
		source: Some("protolint".to_string()),
		..RawDiagnostic::default()
	})
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

fn generic_issue(output: &str) -> RawDiagnostic {
	RawDiagnostic {
		message: output.to_string(),
		source: Some("protolint".to_string()),
		..RawDiagnostic::default()
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn reads_the_printed_level() {
		let out = parse(b"", SAMPLES[0].stderr, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].row, Some(4));
		assert_eq!(out[0].col, Some(3));
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[0].code.as_deref(), Some("FIELD_NAMES_LOWER_SNAKE_CASE"));
		assert_eq!(out[0].severity, Some(severity::INFO));
		assert_eq!(out[0].source.as_deref(), Some("protolint"));
		assert_eq!(out[1].code.as_deref(), Some("MESSAGE_NAMES_UPPER_CAMEL_CASE"));
		assert_eq!(out[1].severity, Some(severity::WARNING));
	}

	#[test]
	fn a_lint_without_a_known_level_has_none() {
		let json = br#"{"lints":[
            {"message":"a","line":1,"column":1,"rule":"INDENT"},
            {"message":"b","line":2,"column":1,"rule":"INDENT","severity":"fatal"},
            {"message":"c","line":3,"column":1,"rule":"INDENT","severity":"error"}
        ]}"#;
		let levels: Vec<_> = parse(b"", json, 1).iter().map(|d| d.severity).collect();
		assert_eq!(levels, [None, None, Some(severity::ERROR)]);
	}

	#[test]
	fn skips_file_names_lower_snake_case() {
		let json = br#"{"lints":[
            {"message":"File name should be lower_snake_case.proto","line":1,"column":1,"rule":"FILE_NAMES_LOWER_SNAKE_CASE"},
            {"message":"real issue","line":2,"column":1,"rule":"INDENT"}
        ]}"#;
		let out = parse(b"", json, 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "real issue");
	}

	#[test]
	fn non_json_output_becomes_a_generic_issue_without_level_or_position() {
		for exit in [0, 2] {
			let out = parse(b"", SAMPLES[1].stderr, exit);
			assert_eq!(out.len(), 1);
			assert_eq!(out[0].row, None);
			assert_eq!(out[0].severity, None);
			assert!(out[0].message.contains("but expected [;]"));
		}
	}

	#[test]
	fn malformed_json_becomes_a_generic_issue_without_level_or_position() {
		for exit in [0, 1] {
			let out = parse(b"", SAMPLES[2].stderr, exit);
			assert_eq!(out.len(), 1);
			assert_eq!(out[0].row, None);
			assert_eq!(out[0].severity, None);
			assert_eq!(out[0].source.as_deref(), Some("protolint"));
		}
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	// protolint 0.57.0 with `severity: note` / `severity: warning` configured for the two rules.
	crate::contract::Sample {
		stdout: b"",
		stderr: br#"{
  "basedir": "/work/protolint",
  "lints": [
    {
      "filename": "a.proto",
      "line": 4,
      "column": 3,
      "message": "Field name \"Text\" must be underscore_separated_names like \"text\"",
      "rule": "FIELD_NAMES_LOWER_SNAKE_CASE",
      "severity": "note"
    },
    {
      "filename": "a.proto",
      "line": 3,
      "column": 1,
      "message": "Message name \"greeting\" must be UpperCamelCase like \"Greeting\"",
      "rule": "MESSAGE_NAMES_UPPER_CAMEL_CASE",
      "severity": "warning"
    }
  ]
}
"#,
		exit: 1,
	},
	// protolint 0.57.0 on a file that does not parse.
	crate::contract::Sample {
		stdout: b"",
		stderr: b"found \"\\\"message\\\"(Token=2, Pos=broken.proto:2:1)\" but expected [;] at /home/runner/go/pkg/mod/github.com/yoheimuta/go-protoparser/v4@v4.14.2/parser/syntax.go:93. Use -v for more details\n",
		exit: 2,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"{\"basedir\": \"/work/protolint\", \"lints\": [{\"filename\": \"a.proto\", \"line\": 4,",
		exit: 0,
	},
];
