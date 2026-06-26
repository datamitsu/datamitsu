//! trivy — find misconfigurations and vulnerabilities. Ported from the none-ls
//! diagnostics/trivy builtin.
//!
//! `trivy config --format json` emits a nested object: `Results[]` each holding a
//! `Misconfigurations[]` array, where every misconfiguration carries an `ID`
//! (code), `Title` (message), `Severity`, and a `CauseMetadata` with
//! `StartLine`/`EndLine`. The builtin reads diagnostics from this JSON; it sets
//! `col = 0` and `source = "trivy"` for every entry. The Lua reads from stderr
//! (trivy historically printed its JSON there), so we accept the JSON from either
//! stream.
use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "trivy",
	description: "Find misconfigurations and vulnerabilities",
	url: "https://github.com/aquasecurity/trivy",
	// The builtin runs trivy against a directory (`.`), not stdin or a temp file.
	operations: &[Operation {
		mode: "lint",
		args: &["config", "--format", "json", "--quiet", "."],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true historically; modern trivy prints to stdout. Try stdout
	// first, fall back to stderr.
	let mut out = parse_stream(stdout);
	if out.is_empty() {
		out = parse_stream(stderr);
	}
	out
}

fn parse_stream(bytes: &[u8]) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(bytes);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let mut diags = Vec::new();
	let Some(results) = get(&value, "Results") else {
		return diags;
	};
	let results = match results {
		JsonValue::Array(items) => items,
		_ => return diags,
	};
	for result in results {
		let Some(JsonValue::Array(miscfgs)) = get(result, "Misconfigurations") else {
			continue;
		};
		for m in miscfgs {
			if let Some(d) = from_misconfiguration(m) {
				diags.push(d);
			}
		}
	}
	diags
}

fn from_misconfiguration(m: &JsonValue) -> Option<RawDiagnostic> {
	// Title is the message; the builtin maps it unconditionally (message is
	// mandatory), so skip an entry without one.
	let message = get_str(m, "Title")?;
	let cause = get(m, "CauseMetadata");
	Some(RawDiagnostic {
		message,
		row: cause.and_then(|c| get_u32(c, "StartLine")),
		end_row: cause.and_then(|c| get_u32(c, "EndLine")),
		col: Some(0),
		severity: get_str(m, "Severity").as_deref().and_then(severity_of),
		source: Some("trivy".to_string()),
		code: get_str(m, "ID"),
		..RawDiagnostic::default()
	})
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"CRITICAL" | "HIGH" => Some(severity::ERROR),
		"MEDIUM" => Some(severity::WARNING),
		"LOW" | "UNKNOWN" => Some(severity::INFO),
		_ => None,
	}
}

fn get<'a>(value: &'a JsonValue, key: &str) -> Option<&'a JsonValue> {
	match value {
		JsonValue::Object(map) => map.get(key),
		_ => None,
	}
}

fn get_str(value: &JsonValue, key: &str) -> Option<String> {
	match get(value, key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn get_u32(value: &JsonValue, key: &str) -> Option<u32> {
	match get(value, key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	const SAMPLE: &[u8] = br#"{
      "Results": [
        {
          "Target": "main.tf",
          "Misconfigurations": [
            {
              "ID": "AVD-AWS-0086",
              "Title": "S3 Bucket has block public ACLs disabled",
              "Severity": "HIGH",
              "CauseMetadata": { "StartLine": 12, "EndLine": 18 }
            },
            {
              "ID": "AVD-AWS-0132",
              "Title": "S3 encryption should use Customer Managed Keys",
              "Severity": "LOW",
              "CauseMetadata": { "StartLine": 3, "EndLine": 3 }
            }
          ]
        }
      ]
    }"#;

	#[test]
	fn parses_nested_misconfigurations() {
		let out = parse(SAMPLE, b"", 0);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "S3 Bucket has block public ACLs disabled");
		assert_eq!(out[0].code.as_deref(), Some("AVD-AWS-0086"));
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].end_row, Some(18));
		assert_eq!(out[0].col, Some(0));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("trivy"));

		assert_eq!(out[1].severity, Some(severity::INFO));
		assert_eq!(out[1].code.as_deref(), Some("AVD-AWS-0132"));
	}

	#[test]
	fn falls_back_to_stderr() {
		let out = parse(b"", SAMPLE, 0);
		assert_eq!(out.len(), 2);
	}

	#[test]
	fn no_results_yields_nothing() {
		assert!(parse(br#"{"Results":[]}"#, b"", 0).is_empty());
		assert!(parse(b"not json", b"", 0).is_empty());
	}
}
