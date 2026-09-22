//! dclint — a Docker Compose linter.
//!
//! Ported from `dclint --formatter json`, which prints its per-file results as
//! they are, one entry per Compose file:
//!
//! ```json
//! [{"filePath":"compose.yaml","errorCount":1,"warningCount":0,
//!   "messages":[{"rule":"no-version-field","type":"error","severity":"minor",
//!                "category":"best-practice","message":"…","line":1,"column":1}]}]
//! ```
//!
//! The trap is the field names: `type` is the level (`error` or `warning`, the
//! only two dclint has), while `severity` is dclint's own impact rating —
//! `critical`, `major`, `minor`, `info` — that says nothing about whether the
//! finding fails the run. A `minor` finding is an error when its rule is set to
//! error. Reading `severity` would misreport both ways, so it is never read.
//!
//! A file that is not valid YAML or not a valid Compose document is reported as
//! one finding under the pseudo-rules `invalid-yaml` / `invalid-schema` /
//! `unknown-error`, and its real rules do not run. Those arrive like any other
//! finding. A run that crashes — a path that does not exist, an invalid config —
//! prints a stack trace to stderr and nothing to stdout, which the core shows
//! raw.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "dclint",
	description: "A command-line tool for validating and enforcing best practices in Docker Compose files.",
	url: "https://github.com/zavoloklom/docker-compose-linter",
	operations: &[Operation {
		mode: "lint",
		args: &["--formatter", "json"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// Lenient: `--debug` logs to stdout through console.debug, ahead of the report.
	crate::tools::json_diag::extract_lenient(stdout, from_report)
}

fn from_report(value: &JsonValue) -> Vec<RawDiagnostic> {
	let JsonValue::Array(results) = value else {
		return Vec::new();
	};
	let mut out = Vec::new();
	for result in results {
		let JsonValue::Object(result) = result else {
			continue;
		};
		let Some(JsonValue::Array(messages)) = result.get("messages") else {
			continue;
		};
		let file = match result.get("filePath") {
			Some(JsonValue::String(s)) => crate::diagnostic::file_field(s),
			_ => None,
		};
		out.extend(messages.iter().filter_map(|message| from_message(message, &file)));
	}
	out
}

fn from_message(value: &JsonValue, file: &Option<String>) -> Option<RawDiagnostic> {
	let JsonValue::Object(m) = value else {
		return None;
	};
	let message = string_field(m, "message")?;
	Some(RawDiagnostic {
		message,
		row: position(m, "line"),
		col: position(m, "column"),
		end_row: position(m, "endLine"),
		end_col: position(m, "endColumn"),
		severity: string_field(m, "type").and_then(|t| level_of(&t)),
		code: string_field(m, "rule"),
		file: file.clone(),
		..RawDiagnostic::default()
	})
}

fn level_of(kind: &str) -> Option<u8> {
	match kind {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		_ => None,
	}
}

fn string_field(m: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match m.get(key) {
		Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
		_ => None,
	}
}

fn position(m: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match m.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n).filter(|&n| n > 0),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	// Verbatim `dclint --formatter json` over four Compose files (`meta`
	// shortened): rule findings at both levels, a file that is not YAML, a file
	// that is not a Compose document, and a clean file.
	const REPORT: &str = r##"[
  {
    "filePath": "compose.yaml",
    "messages": [
      {"rule": "no-unbound-port-interfaces", "type": "error", "category": "security", "severity": "major",
       "message": "Service \"b\" is exporting port \"8080:80\" without specifying the interface to listen on.",
       "line": 4, "column": 1, "meta": {"description": "…", "url": "https://…"}, "fixable": false,
       "data": {"serviceName": "b", "port": "8080:80"}},
      {"rule": "require-project-name-field", "type": "warning", "category": "best-practice", "severity": "minor",
       "message": "The \"name\" field should be present.", "line": 1, "column": 1,
       "meta": {"description": "…", "url": "https://…"}, "fixable": false, "data": {}},
      {"rule": "services-alphabetical-order", "type": "warning", "category": "style", "severity": "minor",
       "message": "Service \"a\" should be before \"b\".", "line": 6, "column": 1,
       "meta": {"description": "…", "url": "https://…"}, "fixable": true, "data": {"serviceName": "a", "misplacedBefore": "b"}}
    ],
    "errorCount": 1,
    "warningCount": 2,
    "fixableErrorCount": 0,
    "fixableWarningCount": 1
  },
  {
    "filePath": "compose.bad-yaml.yaml",
    "messages": [
      {"rule": "invalid-yaml", "category": "style", "severity": "critical", "message": "Invalid YAML format.",
       "line": 4, "column": 1, "type": "error", "fixable": false, "data": {}}
    ],
    "errorCount": 1,
    "warningCount": 0,
    "fixableErrorCount": 0,
    "fixableWarningCount": 0
  },
  {
    "filePath": "compose.schema.yaml",
    "messages": [
      {"rule": "invalid-schema", "type": "error", "category": "style", "severity": "critical",
       "message": "ComposeValidationError: instancePath=\"/services/a\", schemaPath=\"#/additionalProperties\", message=\"Validation error: must NOT have additional properties\".",
       "line": 1, "column": 1, "fixable": false, "data": {}}
    ],
    "errorCount": 1,
    "warningCount": 0,
    "fixableErrorCount": 0,
    "fixableWarningCount": 0
  },
  {
    "filePath": "compose.clean.yaml",
    "messages": [],
    "errorCount": 0,
    "warningCount": 0,
    "fixableErrorCount": 0,
    "fixableWarningCount": 0
  }
]"##;

	fn find<'a>(out: &'a [RawDiagnostic], code: &str) -> &'a RawDiagnostic {
		out
			.iter()
			.find(|d| d.code.as_deref() == Some(code))
			.unwrap_or_else(|| panic!("no diagnostic {code:?} in {out:#?}"))
	}

	#[test]
	fn reports_every_finding_under_its_file() {
		let out = parse(REPORT.as_bytes(), b"", 1);

		assert_eq!(out.len(), 5);
		assert_eq!(
			find(&out, "services-alphabetical-order").file.as_deref(),
			Some("compose.yaml")
		);
		assert_eq!(
			find(&out, "invalid-yaml").file.as_deref(),
			Some("compose.bad-yaml.yaml")
		);
		let ordered = find(&out, "services-alphabetical-order");
		assert_eq!((ordered.row, ordered.col), (Some(6), Some(1)));
		assert_eq!(ordered.message, "Service \"a\" should be before \"b\".");
	}

	#[test]
	fn takes_the_level_from_type_not_from_dclints_impact_rating() {
		let out = parse(REPORT.as_bytes(), b"", 1);

		// `severity: "major"` on an error, `severity: "minor"` on a warning.
		assert_eq!(find(&out, "no-unbound-port-interfaces").severity, Some(severity::ERROR));
		assert_eq!(
			find(&out, "require-project-name-field").severity,
			Some(severity::WARNING)
		);
		// A rule the project raised to error keeps its `minor` rating — the level
		// is what decides the run, so that is what the diagnostic must say.
		let raised = br#"[{"filePath":"c.yaml","messages":[
            {"rule":"services-alphabetical-order","type":"error","severity":"minor","message":"m","line":2,"column":1}]}]"#;
		assert_eq!(parse(raised, b"", 1)[0].severity, Some(severity::ERROR));
		// A level dclint does not have is left for the core to default.
		let unknown = br#"[{"filePath":"c.yaml","messages":[{"rule":"r","type":"critical","message":"m"}]}]"#;
		assert_eq!(parse(unknown, b"", 1)[0].severity, None);
	}

	#[test]
	fn reports_a_file_that_is_not_yaml_or_not_compose_as_a_finding() {
		let out = parse(REPORT.as_bytes(), b"", 1);

		let yaml = find(&out, "invalid-yaml");
		assert_eq!(yaml.message, "Invalid YAML format.");
		assert_eq!((yaml.row, yaml.col), (Some(4), Some(1)));
		assert_eq!(yaml.severity, Some(severity::ERROR));

		let schema = find(&out, "invalid-schema");
		assert!(schema.message.contains("must NOT have additional properties"));
		assert_eq!(schema.file.as_deref(), Some("compose.schema.yaml"));
	}

	#[test]
	fn keeps_an_end_position_when_a_rule_reports_one() {
		let out = parse(
			br#"[{"filePath":"c.yaml","messages":[{"rule":"r","type":"error","message":"m","line":3,"column":5,"endLine":4,"endColumn":2}]}]"#,
			b"",
			1,
		);

		assert_eq!(
			(out[0].row, out[0].col, out[0].end_row, out[0].end_col),
			(Some(3), Some(5), Some(4), Some(2))
		);
	}

	#[test]
	fn reports_nothing_for_a_clean_run() {
		assert!(parse(
			br#"[{"filePath":"compose.yaml","messages":[],"errorCount":0,"warningCount":0}]"#,
			b"",
			0
		)
		.is_empty());
		assert!(parse(b"[]", b"", 0).is_empty());
	}

	#[test]
	fn leaves_a_crash_to_the_raw_output() {
		// Verbatim head of a run over a path that does not exist.
		let stderr = b"FileNotFoundError: File or directory not found: does-not-exist.yaml\n    at findFilesForLinting (dclint.cjs:5949:19)\n";

		assert!(parse(b"", stderr, 1).is_empty());
	}

	#[test]
	fn survives_debug_logging_ahead_of_the_report() {
		let noisy = format!(
			"[DEBUG] [CLI] Debug mode is ON\n[DEBUG] [UTIL] Using built-in formatter: json\n{}",
			REPORT
		);

		assert_eq!(parse(noisy.as_bytes(), b"", 1).len(), 5);
	}

	#[test]
	fn yields_nothing_rather_than_guessing_on_malformed_input() {
		for input in [
			&b""[..],
			b"not json at all",
			br#"[{"filePath":"c.yaml","messages":"#,
			br#"{"filePath":"c.yaml","messages":[{"rule":"r","type":"error","message":"m"}]}"#, // not an array
			br#"[{"filePath":"c.yaml","messages":{}}]"#,
			br#"["c.yaml",42,null]"#,
			br#"[{"filePath":"c.yaml","messages":[{"rule":"r","type":"error","line":1}]}]"#, // no message
		] {
			assert!(
				parse(input, b"", 1).is_empty(),
				"input {:?}",
				String::from_utf8_lossy(input)
			);
		}
	}

	#[test]
	fn drops_a_position_that_is_not_a_usable_line_number() {
		for position in [r#""7""#, "-1", "null", "99999999999", "true", "0"] {
			let json = format!(r#"[{{"filePath":"c.yaml","messages":[{{"rule":"r","message":"m","line":{position}}}]}}]"#);
			let out = parse(json.as_bytes(), b"", 1);
			assert_eq!(out.len(), 1, "line {position}");
			assert_eq!(out[0].row, None, "line {position}");
		}
	}

	#[test]
	fn keeps_a_finding_whose_result_names_no_file() {
		for result in [
			r#"{"messages":[{"rule":"r","message":"m"}]}"#,
			r#"{"filePath":"","messages":[{"rule":"r","message":"m"}]}"#,
		] {
			let out = parse(format!("[{result}]").as_bytes(), b"", 1);
			assert_eq!(out.len(), 1, "result {result}");
			assert_eq!(out[0].file, None, "result {result}");
		}
	}

	#[test]
	fn carries_non_ascii_paths_and_messages_through_intact() {
		let out = parse(
			r#"[{"filePath":"cafés/compose.yaml","messages":[{"rule":"r","type":"error","message":"Service \"naïve\"","line":1,"column":1}]}]"#.as_bytes(),
			b"",
			1,
		);

		assert_eq!(out[0].file.as_deref(), Some("cafés/compose.yaml"));
		assert_eq!(out[0].message, "Service \"naïve\"");
	}
}
