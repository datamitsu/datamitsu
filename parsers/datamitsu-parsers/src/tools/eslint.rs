//! eslint — the pluggable JavaScript/TypeScript linter.
//!
//! eslint is NOT a none-ls diagnostics builtin (it was moved to an external
//! plugin), so this is ported directly from eslint's `--format json` output: an
//! array of result objects, each with a `messages` array. It reads that report
//! through the `eslint-json` format parser, which knows its two wrinkles:
//! diagnostics **nested** under each file's `messages`, and a **numeric**
//! `severity` (2 = error, 1 = warning); a `null` `ruleId` (a parse error) gives
//! no code.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "eslint",
	description: "Pluggable linter for JavaScript and TypeScript.",
	url: "https://eslint.org",
	severities: crate::format::eslint_json::LEVELS,
	column_unit: "utf-16",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// Lenient: eslint runs batched over a whole project, where a plugin writing to
	// stdout (sonarjs' pnpm-catalog `console.debug`, …) would otherwise cost every
	// diagnostic in the run.
	crate::format::eslint_json::findings(stdout)
}

#[cfg(test)]
mod tests {
	use super::*;
	use crate::severity;

	// Real `eslint --format json` output (trimmed).
	pub(super) const SAMPLE: &[u8] = br#"[{"filePath":"/x/broken.js","messages":[
        {"ruleId":"no-unused-vars","severity":2,"message":"'x' is assigned a value but never used.","line":1,"column":5,"endLine":1,"endColumn":6},
        {"ruleId":"semi","severity":1,"message":"Missing semicolon.","line":1,"column":10,"endLine":2,"endColumn":1},
        {"ruleId":null,"severity":2,"message":"Parsing error: Unexpected token","line":3,"column":1}
    ]}]"#;

	#[test]
	fn parses_nested_messages_with_numeric_severity() {
		let out = parse(SAMPLE, b"", 1);
		assert_eq!(out.len(), 3);

		assert_eq!(out[0].message, "'x' is assigned a value but never used.");
		assert_eq!((out[0].row, out[0].col), (Some(1), Some(5)));
		assert_eq!((out[0].end_row, out[0].end_col), (Some(1), Some(6)));
		assert_eq!(out[0].severity, Some(severity::ERROR)); // eslint 2 -> error
		assert_eq!(out[0].code.as_deref(), Some("no-unused-vars"));

		assert_eq!(out[1].severity, Some(severity::WARNING)); // eslint 1 -> warning
		assert_eq!(out[1].code.as_deref(), Some("semi"));

		// null ruleId -> no code; still a diagnostic.
		assert_eq!(out[2].code, None);
		assert_eq!(out[2].message, "Parsing error: Unexpected token");
	}

	#[test]
	fn a_severity_outside_eslints_two_levels_is_left_unset() {
		for severity in ["0", "3", "2.5", r#""2""#, "null"] {
			let json = format!(r#"[{{"filePath":"/x/a.js","messages":[{{"severity":{severity},"message":"m"}}]}}]"#);
			assert_eq!(parse(json.as_bytes(), b"", 1)[0].severity, None, "severity {severity}");
		}
	}

	#[test]
	fn attributes_each_message_to_its_file() {
		let out = parse(SAMPLE, b"", 1);
		assert!(out.iter().all(|d| d.file.as_deref() == Some("/x/broken.js")));
	}

	#[test]
	fn skips_plugin_noise_printed_before_the_json() {
		// eslint-plugin-sonarjs console.debug()s onto stdout ahead of the report.
		let mut noisy = b"Dependency \"@scope/pkg\" could not be resolved for catalog \"default\"\n".to_vec();
		noisy.extend_from_slice(SAMPLE);
		assert_eq!(parse(&noisy, b"", 1).len(), 3);
	}

	#[test]
	fn clean_run_yields_nothing() {
		assert!(parse(br#"[{"filePath":"/x/ok.js","messages":[]}]"#, b"", 0).is_empty());
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: tests::SAMPLE,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"[{"filePath":"/x/ok.js","messages":[]}]"#,
		stderr: b"",
		exit: 0,
	},
];
