//! buildifier — formatter/linter for bazel BUILD, WORKSPACE and .bzl files.
//! Ported from the none-ls diagnostics/buildifier builtin.
//!
//! Output shape (`-format=json`): `{"files":[{"filename":…,"warnings":[{"start":
//! {"line","column"},"end":{"line","column"},"category","message","url"}]}]}`.
//! none-ls picks the file matching the buffer name; since the core lints one file
//! at a time we flatten every file's warnings. The `warnings` key is the level;
//! `category` is the rule and `url` its documentation. Positions are 1-based and
//! `end` names the column after the span. On a parse failure buildifier prints
//! `<path>:<line>:<col>: <message>` to stderr, with no level.
use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "buildifier",
	description: "buildifier is a tool for formatting and linting bazel BUILD, WORKSPACE, and .bzl files.",
	url: "https://github.com/bazelbuild/buildtools/tree/master/buildifier",
	severities: &[Level("warnings", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-mode=check", "-lint=warn", "-format=json", "-path={file}"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	if let Ok(JsonValue::Object(root)) = text.parse::<JsonValue>() {
		if let Some(JsonValue::Array(files)) = root.get("files") {
			let mut out = Vec::new();
			for file in files {
				if let JsonValue::Object(f) = file {
					let key = "warnings";
					if let Some(JsonValue::Array(warnings)) = f.get(key) {
						let level = severity::of(DESCRIPTOR.severities, key);
						let filename = match f.get("filename") {
							Some(JsonValue::String(s)) => crate::diagnostic::exact_file_field(s),
							_ => None,
						};
						for w in warnings {
							if let Some(mut d) = parse_warning(w, level) {
								d.file.clone_from(&filename);
								out.push(d);
							}
						}
					}
				}
			}
			return out;
		}
	}

	// Parse-error path: buildifier could not produce JSON and reported on stderr.
	String::from_utf8_lossy(stderr)
		.lines()
		.filter_map(parse_error_line)
		.collect()
}

fn parse_warning(value: &JsonValue, level: Option<u8>) -> Option<RawDiagnostic> {
	let obj = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	// none-ls only emits a warning when both start and end are present.
	let (row, col) = pos(obj.get("start")?)?;
	let (end_row, end_col) = pos(obj.get("end")?)?;
	Some(RawDiagnostic {
		message: str_field(obj, "message")?,
		row: Some(row),
		col: Some(col),
		end_row: Some(end_row),
		end_col: Some(end_col),
		severity: level,
		source: Some("buildifier".to_string()),
		code: str_field(obj, "category").filter(|c| !c.is_empty()),
		url: str_field(obj, "url").filter(|u| !u.is_empty()),
		..RawDiagnostic::default()
	})
}

fn pos(value: &JsonValue) -> Option<(u32, u32)> {
	let obj = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	Some((u32_field(obj, "line")?, u32_field(obj, "column")?))
}

fn str_field(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn u32_field(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

/// Lua pattern `.-:(%d+):(%d+): (.*)` — last `:line:col: message` on the line.
fn parse_error_line(line: &str) -> Option<RawDiagnostic> {
	let (loc, message) = split_after_loc(line)?;
	let mut it = loc.rsplitn(3, ':');
	let col: u32 = it.next()?.trim().parse().ok()?;
	let row: u32 = it.next()?.trim().parse().ok()?;
	Some(RawDiagnostic {
		message: message.trim().to_string(),
		row: Some(row),
		col: Some(col),
		source: Some("buildifier".to_string()),
		..RawDiagnostic::default()
	})
}

/// Split `prefix:line:col: message` into (`prefix:line:col`, `message`) at the
/// first `": "` that follows two numeric `:`-delimited groups.
fn split_after_loc(line: &str) -> Option<(&str, &str)> {
	// Find ": " separators and test each as the message boundary.
	let bytes = line.as_bytes();
	for i in 0..bytes.len().saturating_sub(1) {
		if bytes[i] == b':' && bytes[i + 1] == b' ' {
			let loc = &line[..i];
			// loc must end with :line:col (two trailing numeric groups).
			let mut it = loc.rsplitn(3, ':');
			let col = it.next();
			let row = it.next();
			if let (Some(c), Some(r)) = (col, row) {
				if !c.is_empty()
					&& c.bytes().all(|b| b.is_ascii_digit())
					&& !r.is_empty()
					&& r.bytes().all(|b| b.is_ascii_digit())
				{
					return Some((loc, &line[i + 2..]));
				}
			}
		}
	}
	None
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_json_warnings() {
		let json = br#"{"files":[{"filename":"BUILD","warnings":[
            {"start":{"line":10,"column":1},"end":{"line":10,"column":20},"category":"load",
             "message":"Loaded symbol is unused.","url":"https://example.com/unused-load"}
        ]}]}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(10));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].end_row, Some(10));
		assert_eq!(out[0].end_col, Some(20));
		assert_eq!(out[0].severity, Some(severity::WARNING));
		assert_eq!(out[0].source.as_deref(), Some("buildifier"));
		assert_eq!(out[0].code.as_deref(), Some("load"));
		assert_eq!(out[0].url.as_deref(), Some("https://example.com/unused-load"));
		assert_eq!(out[0].message, "Loaded symbol is unused.");
	}

	#[test]
	fn a_warning_without_category_or_url_has_neither() {
		let json = br#"{"files":[{"warnings":[
            {"start":{"line":1,"column":1},"end":{"line":1,"column":4},"message":"m"}
        ]}]}"#;
		let out = parse(json, b"", 0);
		assert_eq!((out[0].code.as_deref(), out[0].url.as_deref()), (None, None));
		assert_eq!(out[0].message, "m");
	}

	#[test]
	fn parses_stderr_parse_error_without_a_level() {
		let out = parse(b"", b"/path/to/BUILD:12:5: syntax error near foo\n", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].severity, None);
		assert_eq!(out[0].message, "syntax error near foo");
	}

	#[test]
	fn empty_when_no_warnings() {
		let out = parse(br#"{"files":[{"filename":"BUILD","warnings":[]}]}"#, b"", 0);
		assert!(out.is_empty());
	}
	#[test]
	fn names_the_file_of_each_warning() {
		let json = br#"{"files":[{"filename":"pkg/BUILD","formatted":true,"valid":true,"warnings":[
            {"start":{"line":1,"column":1},"end":{"line":1,"column":5},"category":"load","message":"m","url":"https://example.test/load"}]}]}"#;
		assert_eq!(parse(json, b"", 4)[0].file.as_deref(), Some("pkg/BUILD"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{"success":false,"files":[{"filename":"BUILD","formatted":true,"valid":true,"warnings":[{"start":{"line":1,"column":22},"end":{"line":1,"column":27},"category":"load","actionable":true,"autoFixable":true,"message":"Loaded symbol \"unused\" is unused.","url":"https://github.com/bazelbuild/buildtools/blob/main/WARNINGS.md#load"},{"start":{"line":4,"column":5},"end":{"line":4,"column":8},"category":"unused-variable","actionable":true,"autoFixable":false,"message":"Variable \"foo\" is unused. Please remove it.","url":"https://github.com/bazelbuild/buildtools/blob/main/WARNINGS.md#unused-variable"}]}]}"#,
		stderr: b"",
		exit: 4,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"BUILD:3:14: syntax error near )\n",
		exit: 1,
	},
];
