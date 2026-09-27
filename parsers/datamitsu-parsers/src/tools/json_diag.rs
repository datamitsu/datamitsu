//! Shared JSON → diagnostics helper for the JSON-output tool class.
//!
//! Mirrors none-ls's `h.diagnostics.from_json`: a tool emits a JSON array (or a
//! single object) of diagnostics, and each object's fields map by name onto a
//! [`RawDiagnostic`]. The default attribute names match none-ls
//! (`line`/`column`/`endLine`/`endColumn`/`ruleId`/`message`/`level`); a tool
//! that names them differently overrides only what differs.
//!
//! `tinyjson` is a tiny, zero-dependency pure-Rust parser — the one external
//! crate we accept, because hand-rolling a correct JSON parser (escapes, unicode,
//! nesting) is a known footgun and ~25 tools emit JSON.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::diagnostic::RawDiagnostic;

/// JSON key names for each diagnostic field. Construct from [`Attrs::defaults`]
/// and override the ones a tool spells differently.
pub struct Attrs {
	pub row: &'static str,
	pub col: &'static str,
	pub end_row: &'static str,
	pub end_col: &'static str,
	pub code: &'static str,
	pub message: &'static str,
	pub severity: &'static str,
	/// Key naming the file a diagnostic belongs to. Tools that lint many files
	/// per run must report it; the core cannot infer it from a batch invocation.
	pub file: &'static str,
}

impl Attrs {
	/// none-ls's default attribute names.
	pub const fn defaults() -> Self {
		Attrs {
			row: "line",
			col: "column",
			end_row: "endLine",
			end_col: "endColumn",
			code: "ruleId",
			message: "message",
			severity: "level",
			file: "file",
		}
	}
}

/// Maps a tool's severity token (e.g. "warning", "style") to the 1–4 scale.
/// Returns `None` to leave severity unset (the core supplies its fallback).
pub type SeverityMap = fn(&str) -> Option<u8>;

/// Parse a JSON array — or a single object — of diagnostics. An object missing
/// the message field is skipped (message is the one required field). Invalid JSON
/// yields no diagnostics rather than an error.
pub fn from_json(bytes: &[u8], attrs: &Attrs, sev: SeverityMap) -> Vec<RawDiagnostic> {
	extract_lenient(bytes, |value| {
		let mut out = Vec::new();
		match value {
			JsonValue::Array(items) => {
				for it in items {
					if let Some(d) = from_obj(it, attrs, sev) {
						out.push(d);
					}
				}
			}
			JsonValue::Object(_) => {
				if let Some(d) = from_obj(value, attrs, sev) {
					out.push(d);
				}
			}
			_ => {}
		}
		out
	})
}

/// Run `extract` over the JSON document embedded in a stream that may carry
/// non-JSON noise around it, and return what it yielded.
///
/// A tool's JSON rarely arrives alone on stdout. Something else in the process
/// prints ahead of it (`eslint-plugin-sonarjs` `console.debug`s a pnpm catalog
/// warning; node and python wrappers do the same), or the tool itself appends a
/// human summary after it — `golangci-lint --output.json.path=stdout` follows the
/// report with `1 issues:` and a per-linter tally. A strict parse throws away
/// every diagnostic over either, so instead: find each document opener, scan
/// forward to its balanced close, and parse that span.
///
/// When the whole input parses, that IS the document and its extraction is
/// returned as-is. Otherwise every candidate span is tried and the **best** one
/// wins: most diagnostics first, longest span as the tiebreak. Picking the first
/// span that merely parses is not enough — a `{}` in the leading noise would
/// shadow the real report, and a one-line JSON log entry ahead of it would be
/// reported as a phantom diagnostic in its place.
///
/// A document that never closes yields nothing from that opener; a truncation
/// that leaves whole inner elements intact can still yield those elements, which
/// beats discarding a run's findings over a cut-off tail.
pub fn extract_lenient<T>(bytes: &[u8], extract: impl Fn(&JsonValue) -> Vec<T>) -> Vec<T> {
	let text = String::from_utf8_lossy(bytes);
	if let Ok(v) = text.parse::<JsonValue>() {
		return extract(&v);
	}
	// Bounded so pathological input (a log full of braces) cannot make parsing
	// quadratic over a large buffer.
	const MAX_ATTEMPTS: usize = 16;
	let mut best: Option<((usize, usize), Vec<T>)> = None;
	for (start, _) in text
		.char_indices()
		.filter(|(_, c)| *c == '[' || *c == '{')
		.take(MAX_ATTEMPTS)
	{
		let Some(end) = balanced_end(&text, start) else {
			continue;
		};
		let Ok(value) = text[start..end].parse::<JsonValue>() else {
			continue;
		};
		let out = extract(&value);
		let rank = (out.len(), end - start);
		if best.as_ref().is_none_or(|(best_rank, _)| rank > *best_rank) {
			best = Some((rank, out));
		}
	}
	best.map(|(_, out)| out).unwrap_or_default()
}

/// Byte index just past the value opening at `start`, or `None` if it never
/// closes. Tracks string literals and their escapes, so braces and brackets
/// inside strings (a Windows path, a message quoting JSON) do not shift the
/// depth count.
fn balanced_end(text: &str, start: usize) -> Option<usize> {
	let mut depth = 0usize;
	let mut in_string = false;
	let mut escaped = false;
	for (i, c) in text[start..].char_indices() {
		if in_string {
			match c {
				_ if escaped => escaped = false,
				'\\' => escaped = true,
				'"' => in_string = false,
				_ => {}
			}
			continue;
		}
		match c {
			'"' => in_string = true,
			'{' | '[' => depth += 1,
			'}' | ']' => {
				depth -= 1;
				if depth == 0 {
					return Some(start + i + c.len_utf8());
				}
			}
			_ => {}
		}
	}
	None
}

/// Map one JSON object onto a diagnostic. `None` if it is not an object or has no
/// message. Exposed so a tool that nests its diagnostics (e.g. `{results:[…]}`)
/// can navigate to the objects itself and reuse the field mapping.
pub fn from_obj(value: &JsonValue, attrs: &Attrs, sev: SeverityMap) -> Option<RawDiagnostic> {
	let map = match value {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = get_str(map, attrs.message)?;
	Some(RawDiagnostic {
		message,
		row: get_u32(map, attrs.row),
		col: get_u32(map, attrs.col),
		end_row: get_u32(map, attrs.end_row),
		end_col: get_u32(map, attrs.end_col),
		code: get_str(map, attrs.code),
		severity: get_string_only(map, attrs.severity).and_then(|s| sev(&s)),
		file: get_str(map, attrs.file)
			.as_deref()
			.and_then(crate::diagnostic::file_field),
		..RawDiagnostic::default()
	})
}

/// A string field, coercing a number to its string form (some tools emit numeric
/// codes).
fn get_str(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		Some(JsonValue::Number(n)) => Some(num_to_string(*n)),
		_ => None,
	}
}

/// A string field that must actually be a string (used for the severity token).
fn get_string_only(map: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match map.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

/// A positive integer field, accepting either a JSON number or a numeric string.
fn get_u32(map: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		Some(JsonValue::String(s)) => s.trim().parse().ok(),
		_ => None,
	}
}

fn num_to_string(n: f64) -> String {
	if n.is_finite() && n.fract() == 0.0 {
		(n as i64).to_string()
	} else {
		n.to_string()
	}
}

#[cfg(test)]
mod tests {
	use super::*;
	use crate::severity;

	fn sev(level: &str) -> Option<u8> {
		match level {
			"error" => Some(severity::ERROR),
			"warning" => Some(severity::WARNING),
			_ => None,
		}
	}

	#[test]
	fn maps_array_of_objects_with_default_attrs() {
		let json = br#"[{"line":3,"column":7,"ruleId":"R1","level":"error","message":"boom"}]"#;
		let out = from_json(json, &Attrs::defaults(), sev);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "boom");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(7));
		assert_eq!(out[0].code.as_deref(), Some("R1"));
		assert_eq!(out[0].severity, Some(severity::ERROR));
	}

	#[test]
	fn object_without_message_is_skipped() {
		let json = br#"[{"line":1,"level":"error"},{"line":2,"message":"ok"}]"#;
		let out = from_json(json, &Attrs::defaults(), sev);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "ok");
	}

	#[test]
	fn numeric_code_is_stringified() {
		let json = br#"[{"message":"x","ruleId":1234}]"#;
		let out = from_json(json, &Attrs::defaults(), sev);
		assert_eq!(out[0].code.as_deref(), Some("1234"));
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(from_json(b"not json", &Attrs::defaults(), sev).is_empty());
	}

	#[test]
	fn single_object_is_accepted() {
		let out = from_json(br#"{"message":"solo","line":5}"#, &Attrs::defaults(), sev);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, Some(5));
	}
	#[test]
	fn parse_lenient_handles_noise_around_the_document() {
		let attrs = Attrs::defaults();

		// Leading noise (a plugin writing to stdout before the report).
		let leading = br#"Dependency "x" could not be resolved
[{"message":"m","line":1,"column":2}]"#;
		assert_eq!(from_json(leading, &attrs, sev).len(), 1);

		// Trailing summary (golangci-lint prints a tally after its JSON).
		let trailing = br#"{"message":"m","line":1,"column":2}
1 issues:
* errcheck: 1"#;
		assert_eq!(from_json(trailing, &attrs, sev).len(), 1);

		// Both at once.
		let both = br#"go: downloading example.com/m v1.0.0
[{"message":"m","line":3,"column":4}]
2 issues:"#;
		let out = from_json(both, &attrs, sev);
		assert_eq!(out.len(), 1);
		assert_eq!((out[0].row, out[0].col), (Some(3), Some(4)));
	}

	#[test]
	fn parse_lenient_is_not_fooled_by_braces_inside_strings() {
		// A brace inside a string must not close the document early.
		let tricky = br#"noise
[{"message":"use {} instead of \"[]\" here","line":1,"column":1}]
trailing"#;
		let out = from_json(tricky, &Attrs::defaults(), sev);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, r#"use {} instead of "[]" here"#);
	}

	#[test]
	fn nothing_is_extracted_when_no_document_closes() {
		let attrs = Attrs::defaults();
		// An object cut off mid-field never closes: no span, nothing extracted.
		assert!(from_json(br#"[{"message":"m","line":1"#, &attrs, sev).is_empty());
		assert!(from_json(b"not json at all", &attrs, sev).is_empty());
		assert!(from_json(b"", &attrs, sev).is_empty());
	}

	#[test]
	fn a_truncation_that_leaves_whole_elements_keeps_them() {
		// The outer array never closes, but the first element did — reporting it
		// beats discarding the run over a cut-off tail.
		let cut = br#"[{"message":"first","line":1},{"message":"secon"#;
		let out = from_json(cut, &Attrs::defaults(), sev);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "first");
	}

	#[test]
	fn empty_json_in_the_noise_does_not_shadow_the_real_report() {
		// Picking the first span that merely parses would return 0 diagnostics.
		let shadowed = br#"cache: {}
[{"message":"real","line":7}]"#;
		let out = from_json(shadowed, &Attrs::defaults(), sev);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "real");
	}

	#[test]
	fn a_json_log_line_ahead_of_the_report_is_not_reported_as_a_diagnostic() {
		// Both spans yield one diagnostic, so the longer (the real report) wins.
		let logged = br#"{"message":"log line"}
[{"message":"real","line":1,"column":2}]"#;
		let out = from_json(logged, &Attrs::defaults(), sev);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "real");
	}
}
