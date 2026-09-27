//! The raw, nullable parser output form.
//!
//! ⚠ **Shape placeholder — finalized in Phase 2.** This is the tentative layout
//! of what a parser extracts, not the final `Diagnostic` struct (that is owned by
//! the Go core and designed in a later phase). The only commitment held here is
//! the nullable contract: **only `message` is mandatory**; every other field is
//! `Option<T>`, where `None` = "the tool did not emit this" (analysis.md §1).
//!
//! Serialized as JSON (debuggable; MessagePack would be premature). `None` fields
//! are omitted entirely so the wire form stays minimal and unambiguous.
//!
//! JSON is hand-written rather than pulled from `serde_json` to keep the WASM
//! artifact in the ~15-30KB range (revise.txt §2.1) — there is no dependency.

/// A single parsed diagnostic, with every field but `message` optional.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct RawDiagnostic {
	/// The human-readable message. The one mandatory field.
	pub message: String,
	/// 1-based line, if the tool reported one. A parser whose tool counts from
	/// 0 adds 1: the core cannot tell the two apart.
	pub row: Option<u32>,
	/// 1-based column, if the tool reported one, counted in the descriptor's
	/// `column_unit`.
	pub col: Option<u32>,
	/// 1-based end line of the span, if the tool reported one.
	pub end_row: Option<u32>,
	/// 1-based **exclusive** end column — the span stops before it. A tool
	/// that prints the last column of the span has 1 added; one that prints no
	/// end leaves this `None` rather than having one invented.
	pub end_col: Option<u32>,
	/// The level on the shared scale, read through the tool's descriptor
	/// vocabulary from a token the tool printed (`crate::severity::of`); `None`
	/// when it printed none.
	pub severity: Option<u8>,
	/// The tool's name, when the parser names it; never a rule.
	pub source: Option<String>,
	/// The rule the finding breaks, whenever the tool prints one. It is part of
	/// a finding's identity, so it carries the rule id alone — no location, no
	/// message text.
	pub code: Option<String>,
	/// A URL documenting the rule, where the tool prints one.
	pub url: Option<String>,
	/// The file the diagnostic belongs to, when the tool's format names one
	/// (eslint's `filePath`, …). Batch tools lint many files per run, so without
	/// this the core cannot attribute a diagnostic; per-file tools leave it `None`
	/// and the core stamps the file it linted.
	pub file: Option<String>,
}

/// Normalize a path a tool printed into the `file` field.
///
/// Returns `None` for anything that does not name a real file — a blank string,
/// or one of the names tools print when they read stdin (`-`, `stdin`,
/// `<stdin>`). Those must stay absent so the core stamps the file it actually
/// linted instead of showing a placeholder. A tool that names its buffer another
/// way (vale's `stdin.<ext>`) filters that name itself.
///
/// A real name is kept byte for byte: trimming could turn ` a.py` into another
/// file of the same batch. A parser whose format pads the name with layout (cue's
/// indented location lines) strips that layout before calling this.
pub fn file_field(raw: &str) -> Option<String> {
	match raw.trim() {
		"" | "-" | "<stdin>" | "stdin" => None,
		_ => Some(raw.to_string()),
	}
}

impl RawDiagnostic {
	/// Serialize to a JSON object, omitting any `None` field.
	pub fn to_json(&self) -> String {
		let mut parts: Vec<String> = Vec::new();
		parts.push(format!(r#""message":{}"#, json_string(&self.message)));
		if let Some(v) = self.row {
			parts.push(format!(r#""row":{v}"#));
		}
		if let Some(v) = self.col {
			parts.push(format!(r#""col":{v}"#));
		}
		if let Some(v) = self.end_row {
			parts.push(format!(r#""end_row":{v}"#));
		}
		if let Some(v) = self.end_col {
			parts.push(format!(r#""end_col":{v}"#));
		}
		if let Some(v) = self.severity {
			parts.push(format!(r#""severity":{v}"#));
		}
		if let Some(v) = &self.source {
			parts.push(format!(r#""source":{}"#, json_string(v)));
		}
		if let Some(v) = &self.code {
			parts.push(format!(r#""code":{}"#, json_string(v)));
		}
		if let Some(v) = &self.url {
			parts.push(format!(r#""url":{}"#, json_string(v)));
		}
		if let Some(v) = &self.file {
			parts.push(format!(r#""file":{}"#, json_string(v)));
		}
		format!("{{{}}}", parts.join(","))
	}
}

/// Serialize a slice of diagnostics to a JSON array.
pub fn to_json_array(diags: &[RawDiagnostic]) -> String {
	let items: Vec<String> = diags.iter().map(RawDiagnostic::to_json).collect();
	format!("[{}]", items.join(","))
}

/// Escape a string into a JSON string literal (including the surrounding quotes).
/// Shared with the `capabilities` module so `describe` hand-writes JSON the same
/// way (no serde dependency — keeps the WASM artifact small).
pub(crate) fn json_string(s: &str) -> String {
	let mut out = String::with_capacity(s.len() + 2);
	out.push('"');
	for c in s.chars() {
		match c {
			'"' => out.push_str("\\\""),
			'\\' => out.push_str("\\\\"),
			'\n' => out.push_str("\\n"),
			'\r' => out.push_str("\\r"),
			'\t' => out.push_str("\\t"),
			c if (c as u32) < 0x20 => out.push_str(&format!("\\u{:04x}", c as u32)),
			c => out.push(c),
		}
	}
	out.push('"');
	out
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn only_message_serializes_with_no_optional_fields() {
		let d = RawDiagnostic {
			message: "boom".to_string(),
			..Default::default()
		};
		assert_eq!(d.to_json(), r#"{"message":"boom"}"#);
	}

	#[test]
	fn present_fields_are_included_absent_omitted() {
		let d = RawDiagnostic {
			message: "x".to_string(),
			row: Some(3),
			col: Some(7),
			severity: Some(1),
			source: Some("hadolint".to_string()),
			code: Some("DL3008".to_string()),
			..Default::default()
		};
		assert_eq!(
			d.to_json(),
			r#"{"message":"x","row":3,"col":7,"severity":1,"source":"hadolint","code":"DL3008"}"#
		);
	}

	#[test]
	fn a_rule_url_serializes_after_the_code() {
		let d = RawDiagnostic {
			message: "x".to_string(),
			code: Some("R1".to_string()),
			url: Some("https://example.test/R1".to_string()),
			..Default::default()
		};
		assert_eq!(
			d.to_json(),
			r#"{"message":"x","code":"R1","url":"https://example.test/R1"}"#
		);
	}

	#[test]
	fn strings_are_json_escaped() {
		let d = RawDiagnostic {
			message: "a\"b\\c\nd\te".to_string(),
			..Default::default()
		};
		assert_eq!(d.to_json(), r#"{"message":"a\"b\\c\nd\te"}"#);
	}

	#[test]
	fn control_chars_escape_as_unicode() {
		let d = RawDiagnostic {
			message: "\u{0001}".to_string(),
			..Default::default()
		};
		assert_eq!(d.to_json(), r#"{"message":"\u0001"}"#);
	}

	#[test]
	fn empty_array_for_no_diagnostics() {
		assert_eq!(to_json_array(&[]), "[]");
	}
	#[test]
	fn file_field_drops_placeholders_but_keeps_real_paths_as_printed() {
		for placeholder in ["", "  ", "-", "<stdin>", "stdin"] {
			assert_eq!(file_field(placeholder), None, "{placeholder:?} is not a file");
		}
		for path in ["docs/stdin.md", "stdin.py", "a/stdin", "src/a.ts", " a.py", "a.py "] {
			assert_eq!(file_field(path).as_deref(), Some(path));
		}
	}
}
