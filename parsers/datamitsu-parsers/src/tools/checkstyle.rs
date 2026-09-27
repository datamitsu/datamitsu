//! checkstyle — Java code-style checker, SARIF output. Ported from the none-ls
//! diagnostics/checkstyle builtin.
//!
//! checkstyle is invoked with `-f sarif`; the parser navigates the SARIF tree
//! (`runs[0].results[].locations[].physicalLocation.region`) rather than the flat
//! none-ls `from_json` shape, so the JSON is walked by hand here. The level is
//! the SARIF `level` checkstyle derives from the check's severity (`error`,
//! `warning`, `note` for info). The region carries only a 1-based start. stderr
//! is also inspected: a missing-config message and the benign "Checkstyle ends
//! with N errors." summary line are special-cased exactly as the builtin does;
//! a stderr finding has no level.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "checkstyle",
	description: "Checkstyle is a tool for checking Java source code for adherence to a Code \
        Standard or set of validation rules (best practices).",
	url: "https://checkstyle.org",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("note", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-f", "sarif", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let mut out = parse_stderr(stderr);
	parse_sarif(stdout, &mut out);
	out
}

/// stderr handling mirrors `parse_checkstyle_errors`: a config-missing hint, a
/// suppressed summary line, otherwise the trimmed stderr as a single finding.
fn parse_stderr(stderr: &[u8]) -> Vec<RawDiagnostic> {
	let err = String::from_utf8_lossy(stderr);
	let trimmed = err.trim();
	if trimmed.is_empty() {
		return Vec::new();
	}
	if err.contains("Must specify a config XML file.") {
		return vec![RawDiagnostic {
			message: "You need to specify a configuration for checkstyle. See \
                https://github.com/nvimtools/none-ls.nvim/blob/main/doc/BUILTINS.md#checkstyle"
				.to_string(),
			..RawDiagnostic::default()
		}];
	}
	// "Checkstyle ends with N errors." is the benign run summary — drop it.
	if is_summary_line(trimmed) {
		return Vec::new();
	}
	vec![RawDiagnostic {
		message: trimmed.to_string(),
		..RawDiagnostic::default()
	}]
}

/// Matches the Lua pattern `Checkstyle ends with %d+ errors.`.
fn is_summary_line(s: &str) -> bool {
	let Some(rest) = s.strip_prefix("Checkstyle ends with ") else {
		return false;
	};
	let Some(digits) = rest.strip_suffix(" errors.") else {
		return false;
	};
	!digits.is_empty() && digits.bytes().all(|b| b.is_ascii_digit())
}

fn parse_sarif(stdout: &[u8], out: &mut Vec<RawDiagnostic>) {
	let text = String::from_utf8_lossy(stdout);
	let root: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return,
	};
	let Some(results) = get(&root, "runs")
		.and_then(|runs| as_array(runs)?.first())
		.and_then(|run| get(run, "results"))
		.and_then(as_array)
	else {
		return;
	};
	for result in results {
		let message = get(result, "message").and_then(|m| get(m, "text")).and_then(as_str);
		let Some(message) = message else {
			continue;
		};
		let code = get(result, "ruleId").and_then(as_str);
		let severity = get(result, "level")
			.and_then(as_str)
			.and_then(|l| severity::of(DESCRIPTOR.severities, &l));

		let locations = get(result, "locations").and_then(as_array);
		let Some(locations) = locations else {
			continue;
		};
		for location in locations {
			let region = get(location, "physicalLocation").and_then(|p| get(p, "region"));
			let col = region.and_then(|r| get(r, "startColumn")).and_then(as_u32);
			let row = region.and_then(|r| get(r, "startLine")).and_then(as_u32);
			out.push(RawDiagnostic {
				message: message.clone(),
				row,
				col,
				code: code.clone(),
				severity,
				..RawDiagnostic::default()
			});
		}
	}
}

fn get<'a>(v: &'a JsonValue, key: &str) -> Option<&'a JsonValue> {
	match v {
		JsonValue::Object(m) => m.get(key),
		_ => None,
	}
}

fn as_array(v: &JsonValue) -> Option<&Vec<JsonValue>> {
	match v {
		JsonValue::Array(a) => Some(a),
		_ => None,
	}
}

fn as_str(v: &JsonValue) -> Option<String> {
	match v {
		JsonValue::String(s) => Some(s.clone()),
		_ => None,
	}
}

fn as_u32(v: &JsonValue) -> Option<u32> {
	match v {
		JsonValue::Number(n) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	const SARIF: &str = r#"{
      "runs": [
        {
          "results": [
            {
              "ruleId": "LineLength",
              "level": "warning",
              "message": { "text": "Line is longer than 80 characters." },
              "locations": [
                {
                  "physicalLocation": {
                    "artifactLocation": { "uri": "file:/src/Main.java" },
                    "region": { "startLine": 12, "startColumn": 81 }
                  }
                }
              ]
            }
          ]
        }
      ]
    }"#;

	fn with_level(level: &str) -> String {
		SARIF.replace(r#""level": "warning""#, &format!(r#""level": "{level}""#))
	}

	#[test]
	fn parses_sarif_result() {
		let out = parse(SARIF.as_bytes(), b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Line is longer than 80 characters.");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(81));
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[0].code.as_deref(), Some("LineLength"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn reads_the_sarif_level() {
		for (level, want) in [
			("error", Some(severity::ERROR)),
			("warning", Some(severity::WARNING)),
			("note", Some(severity::INFO)),
			("none", None),
		] {
			let out = parse(with_level(level).as_bytes(), b"", 1);
			assert_eq!(out[0].severity, want, "{level}");
		}
		let without = SARIF.replace(r#""level": "warning","#, "");
		assert_eq!(parse(without.as_bytes(), b"", 1)[0].severity, None);
	}

	#[test]
	fn summary_line_is_suppressed() {
		let out = parse(b"{}", b"Checkstyle ends with 3 errors.", 3);
		assert!(out.is_empty());
	}

	#[test]
	fn missing_config_yields_a_hint_without_a_level() {
		let out = parse(b"", b"Must specify a config XML file.", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, None);
		assert!(out[0].message.contains("configuration for checkstyle"));
	}

	#[test]
	fn other_stderr_is_one_finding_without_a_level() {
		let out = parse(
			b"",
			b"com.puppycrawl.tools.checkstyle.api.CheckstyleException: unable to parse\n",
			254,
		);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].severity, None);
		assert_eq!(
			out[0].message,
			"com.puppycrawl.tools.checkstyle.api.CheckstyleException: unable to parse"
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{"$schema":"https://json.schemastore.org/sarif-2.1.0.json","version":"2.1.0","runs":[{"tool":{"driver":{"name":"Checkstyle","version":"10.12.4"}},"results":[{"level":"warning","locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:/work/src/Main.java"},"region":{"startColumn":81,"startLine":12}}}],"message":{"text":"Line is longer than 80 characters (found 97)."},"ruleId":"maxLineLen"},{"level":"error","locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:/work/src/Main.java"},"region":{"startLine":1}}}],"message":{"text":"Missing a Javadoc comment."},"ruleId":"javadoc.missing"},{"level":"note","locations":[{"physicalLocation":{"artifactLocation":{"uri":"file:/work/src/Main.java"},"region":{"startColumn":5,"startLine":20}}}],"message":{"text":"'method def modifier' has incorrect indentation level 4, expected level should be 2."},"ruleId":"indentation.child.error"}]}]}"#,
		stderr: b"Checkstyle ends with 1 errors.\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"Must specify a config XML file.\n",
		exit: 254,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"com.puppycrawl.tools.checkstyle.api.CheckstyleException: Exception was thrown while processing /work/src/Main.java\n",
		exit: 254,
	},
];
