//! JSON reports retain filenames and rule codes; --parsing-errors exposes failures with no rule.
//! Identical start/end coordinates represent points, so the JSON reporter supplies no span.
//! Legacy GitHub annotations remain readable from stderr.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};
use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "sqruff",
	description: "A high-speed SQL linter written in Rust.",
	url: "https://github.com/quarylabs/sqruff",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("notice", severity::INFO),
		Level("Error", severity::ERROR),
		Level("Warning", severity::WARNING),
		Level("Information", severity::INFO),
		Level("Hint", severity::HINT),
	],
	column_unit: "utf-32",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["lint", "--format", "json", "--parsing-errors", "{files}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	if text
		.trim()
		.strip_suffix(" files processed, nothing to fix.")
		.is_some_and(|n| n.parse::<u32>().is_ok())
	{
		return Vec::new();
	}
	if let Some(out) = crate::json_diag::find_envelope(stdout, from_json) {
		return out;
	}
	// from_stderr = true: diagnostics come from stderr.
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn from_json(value: &JsonValue) -> Option<Vec<RawDiagnostic>> {
	let JsonValue::Object(files) = value else {
		return None;
	};
	if !files.values().all(|v| matches!(v, JsonValue::Array(_))) {
		return None;
	}
	let mut keys: Vec<_> = files.keys().collect();
	keys.sort();
	let mut out = Vec::new();
	for file in keys {
		let JsonValue::Array(findings) = &files[file] else {
			continue;
		};
		for finding in findings {
			let Some(message) = crate::json_diag::member(finding, "message").and_then(|v| v.get::<String>()) else {
				continue;
			};
			let range = crate::json_diag::member(finding, "range");
			let point = |key: &str| {
				let start = range.and_then(|v| crate::json_diag::member(v, "start"));
				start
					.and_then(|v| crate::json_diag::member(v, key))
					.and_then(|v| v.get::<f64>())
					.and_then(|n| crate::numconv::json_u32(*n))
			};
			out.push(RawDiagnostic {
				message: message.clone(),
				file: crate::diagnostic::file_field(file),
				row: point("line"),
				col: point("character"),
				code: crate::json_diag::member(finding, "code")
					.and_then(|v| v.get::<String>())
					.cloned(),
				severity: crate::json_diag::member(finding, "severity")
					.and_then(|v| v.get::<String>())
					.and_then(|s| severity::of(DESCRIPTOR.severities, s)),
				source: Some(DESCRIPTOR.name.into()),
				..RawDiagnostic::default()
			});
		}
	}
	Some(out)
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// ::<severity> <...>,file=<file>,line=<row>,col=<col>::<message>
	let rest = line.strip_prefix("::")?;

	// severity = first %w+ token, up to the first space.
	let sp = rest.find(' ')?;
	let severity_tok = &rest[..sp];
	if severity_tok.is_empty() || !severity_tok.chars().all(|c| c.is_alphanumeric() || c == '_') {
		return None;
	}

	// Split the annotation header (before "::") from the message (after).
	let close = rest.find("::")?;
	let header = &rest[..close];

	// The Lua pattern requires the message to be `%w+: .*` (a word + ": ").
	let (code, message) = split_rule(&rest[close + 2..])?;

	let row = field_after(header, ",line=")?;
	let col = field_after(header, ",col=")?;
	let file = file_after(header)?;

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity::of(DESCRIPTOR.severities, severity_tok),
		code: Some(code.to_string()),
		file: crate::diagnostic::file_field(&file),
		..RawDiagnostic::default()
	})
}

/// The `file=` property, which the pattern requires. It runs to the next
/// property; a workflow command escapes `,` `:` and `%` inside a value.
fn file_after(header: &str) -> Option<String> {
	let start = header.find(",file=")? + ",file=".len();
	let value = header[start..].split(',').next()?;
	Some(value.replace("%2C", ",").replace("%3A", ":").replace("%25", "%"))
}

/// Splits the Lua `(%w+: .*)` message capture into the rule and its text.
fn split_rule(message: &str) -> Option<(&str, &str)> {
	let (word, text) = message.split_once(": ")?;
	let is_rule = !word.is_empty() && word.chars().all(|c| c.is_alphanumeric() || c == '_');
	is_rule.then_some((word, text))
}

/// Parses the unsigned integer immediately following `key` in `header`.
fn field_after(header: &str, key: &str) -> Option<u32> {
	let start = header.find(key)? + key.len();
	let digits: String = header[start..].chars().take_while(|c| c.is_ascii_digit()).collect();
	digits.parse().ok()
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn json_preserves_files_parse_errors_and_printed_levels() {
		let json = br#"{"bad.sql":[{"range":{"start":{"line":1,"character":7},"end":{"line":1,"character":7}},"message":"Spacing","severity":"Warning","source":"sqruff","code":"LT01"}],"invalid.sql":[{"range":{"start":{"line":1,"character":1}},"message":"Unparsable section","severity":"Warning","code":null}]}"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].file.as_deref(), Some("bad.sql"));
		assert_eq!(out[0].code.as_deref(), Some("LT01"));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].source.as_deref(), Some("sqruff"));
		assert_eq!(out[0].end_col, None);
		assert_eq!(out[1].code, None);
		assert_eq!(out[1].message, "Unparsable section");
	}

	#[test]
	fn clean_json_is_recognized_and_truncated_json_is_not() {
		assert!(
			crate::tools::answer("sqruff", b"1 files processed, nothing to fix.\n", b"", 0)
				.unwrap()
				.recognized
		);
		assert!(
			!crate::tools::answer("sqruff", b"an arbitrary success message", b"", 0)
				.unwrap()
				.recognized
		);
		assert!(crate::tools::answer("sqruff", b"{}", b"", 0).unwrap().recognized);
		assert!(
			!crate::tools::answer("sqruff", br#"{"bad.sql":["#, b"", 1)
				.unwrap()
				.recognized
		);
	}

	#[test]
	fn parses_error_annotation() {
		let d =
			parse_line("::error title=sqruff,file=test.sql,line=1,col=1::CP01: Keywords must be consistently upper case.")
				.unwrap();
		assert_eq!(d.message, "Keywords must be consistently upper case.");
		assert_eq!(d.code.as_deref(), Some("CP01"));
		assert_eq!(d.row, Some(1));
		assert_eq!(d.col, Some(1));
		assert_eq!(d.end_col, None);
		assert_eq!(d.severity, Some(severity::ERROR));
	}

	#[test]
	fn parse_reads_stderr_and_collects() {
		let out = parse(b"", SAMPLES[0].stderr, 1);
		assert_eq!(out.len(), 2);
		assert_eq!(out[1].row, Some(10));
		assert_eq!(out[1].col, Some(3));
		assert_eq!(out[1].message, "Keywords must be lower case.");
		assert_eq!(out[1].code.as_deref(), Some("CP01"));
	}

	#[test]
	fn reads_the_other_annotation_levels() {
		let stderr = b"::warning title=sqruff,file=a.sql,line=2,col=1::LT01: w\n\
::notice title=sqruff,file=a.sql,line=3,col=1::LT02: n\n\
::debug title=sqruff,file=a.sql,line=4,col=1::LT03: d\n";
		let levels: Vec<_> = parse(b"", stderr, 1).iter().map(|d| d.severity).collect();
		assert_eq!(levels, [Some(severity::WARNING), Some(severity::INFO), None]);
	}

	#[test]
	fn non_annotation_line_is_skipped() {
		assert!(parse_line("some unrelated output").is_none());
		// message without the `word: ` rule prefix fails the pattern.
		assert!(parse_line("::error file=a.sql,line=1,col=1::just a message").is_none());
	}

	#[test]
	fn each_finding_names_its_file() {
		let stderr = b"::error title=sqruff,file=models/a.sql,line=1,col=1::LT01: first\n\
::warning title=sqruff,file=b%2Cc.sql,line=2,col=1::LT02: second\n";
		let files: Vec<_> = parse(b"", stderr, 1).into_iter().map(|d| d.file).collect();
		assert_eq!(files, [Some("models/a.sql".to_string()), Some("b,c.sql".to_string())]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"",
	stderr: b"::error title=sqruff,file=a.sql,line=2,col=5::AM04: Query produces an unknown number of result columns.\n\
::error title=sqruff,file=a.sql,line=10,col=3::CP01: Keywords must be lower case.\n",
	exit: 1,
}];
