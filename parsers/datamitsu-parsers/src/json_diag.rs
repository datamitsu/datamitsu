//! Shared JSON → diagnostics helper for the JSON-output tool class and the
//! JSON formats.
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

use std::cell::Cell;
use std::collections::HashMap;

use tinyjson::JsonValue;

thread_local! {
	/// Whether the parse under way found a JSON document in the output: a JSON
	/// tool parser's criterion for recognizing what it read, even when the
	/// document holds no finding. Cleared by [`begin_parse`] before every parse.
	static DOCUMENT_SEEN: Cell<bool> = const { Cell::new(false) };
}

/// Forget whether an earlier parse found a JSON document.
pub(crate) fn begin_parse() {
	DOCUMENT_SEEN.with(|seen| seen.set(false));
}

/// Whether the parse under way found a JSON document.
#[cfg(feature = "tools")]
pub(crate) fn document_seen() -> bool {
	DOCUMENT_SEEN.with(Cell::get)
}

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
#[cfg(feature = "tools")]
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
#[cfg(feature = "tools")]
pub fn extract_lenient<T>(bytes: &[u8], extract: impl Fn(&JsonValue) -> Vec<T>) -> Vec<T> {
	search(bytes, |v| Some(extract(v)), false).unwrap_or_default()
}

/// Like [`extract_lenient`], for a format recognized by its envelope rather than
/// by its findings: `extract` answers `None` for a document that is not the
/// format's envelope, and so does this when no document in the stream is one.
/// A document that parses but is not an envelope is skipped, so a `{}` in the
/// noise cannot stand in for the report. A value inside a document cut off
/// before it closes is part of that document, not one of its own: the search
/// ends at the cut, so the messages array of a cut-off ESLint report is not
/// read as another format.
pub fn find_envelope<T>(bytes: &[u8], extract: impl Fn(&JsonValue) -> Option<Vec<T>>) -> Option<Vec<T>> {
	search(bytes, extract, true)
}

/// The best document `extract` makes something of; with `whole`, only among
/// the documents before the first one cut off.
fn search<T>(bytes: &[u8], extract: impl Fn(&JsonValue) -> Option<Vec<T>>, whole: bool) -> Option<Vec<T>> {
	let text = String::from_utf8_lossy(bytes);
	if let Some(Ok(v)) = parse(&text) {
		DOCUMENT_SEEN.with(|seen| seen.set(true));
		return extract(&v);
	}
	let mut best: Option<((usize, usize), Vec<T>)> = None;
	for (start, end) in spans(&text) {
		let Some(end) = end else {
			if whole && runs_to_end(&text[start..]) {
				break;
			}
			continue;
		};
		let Some(Ok(value)) = parse(&text[start..end]) else {
			continue;
		};
		DOCUMENT_SEEN.with(|seen| seen.set(true));
		let Some(out) = extract(&value) else {
			continue;
		};
		let rank = (out.len(), end - start);
		if best.as_ref().is_none_or(|(best_rank, _)| rank > *best_rank) {
			best = Some((rank, out));
		}
	}
	best.map(|(_, out)| out)
}

/// Each value opener in `text` with the end of its balanced span, `None` when
/// it never closes. Bounded by the bytes the scans read rather than by the
/// openers, so a log whose every line starts `[INFO]` still reaches the
/// document after it, while one full of braces that never close cannot make
/// parsing quadratic over a large buffer.
fn spans(text: &str) -> impl Iterator<Item = (usize, Option<usize>)> + '_ {
	const BUDGET_PER_BYTE: usize = 16;
	let budget = text.len().saturating_mul(BUDGET_PER_BYTE).max(1 << 16);
	let mut spent = 0usize;
	text
		.char_indices()
		.filter(|(_, c)| *c == '[' || *c == '{')
		.map_while(move |(start, _)| {
			if spent > budget {
				return None;
			}
			let end = balanced_end(text, start);
			spent += end.unwrap_or(text.len()) - start;
			Some((start, end))
		})
}

/// Whether the stream holds a JSON document cut off before it closes: a value
/// that runs to the end of the input without a syntax error. Prose that
/// merely holds a bracket breaks off at a character no JSON value takes.
pub(crate) fn cut(bytes: &[u8]) -> bool {
	let text = String::from_utf8_lossy(bytes);
	if matches!(parse(&text), Some(Ok(_))) {
		return false;
	}
	let cut = spans(&text).any(|(start, end)| end.is_none() && runs_to_end(&text[start..]));
	cut
}

/// Whether `value` is JSON up to its end and stops there, unfinished.
fn runs_to_end(value: &str) -> bool {
	parse(value).is_some_and(|r| r.is_err_and(|e| e.to_string().ends_with("Unexpected EOF")))
}

/// Deeper than this a value is not parsed: tinyjson recurses once per level,
/// and a pathological nesting would exhaust the module's stack and trap. A
/// report nests a dozen levels.
const MAX_DEPTH: usize = 256;

/// `text` parsed as JSON; `None` when it nests deeper than [`MAX_DEPTH`].
fn parse(text: &str) -> Option<Result<JsonValue, tinyjson::JsonParseError>> {
	(depth(text) <= MAX_DEPTH).then(|| text.parse::<JsonValue>())
}

/// The deepest nesting of arrays and objects in `text`, outside strings.
fn depth(text: &str) -> usize {
	let (mut depth, mut deepest) = (0usize, 0usize);
	let mut in_string = false;
	let mut escaped = false;
	for c in text.chars() {
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
			'{' | '[' => {
				depth += 1;
				deepest = deepest.max(depth);
			}
			'}' | ']' => depth = depth.saturating_sub(1),
			_ => {}
		}
	}
	deepest
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

/// The member `key` of an object; `None` for anything else.
pub(crate) fn member<'a>(v: &'a JsonValue, key: &str) -> Option<&'a JsonValue> {
	match v {
		JsonValue::Object(m) => m.get(key),
		_ => None,
	}
}

/// The member at `path`, one key per level.
pub(crate) fn at<'a>(v: &'a JsonValue, path: &[&str]) -> Option<&'a JsonValue> {
	path.iter().try_fold(v, |v, key| member(v, key))
}

/// The elements of an array.
pub(crate) fn elements(v: &JsonValue) -> Option<&Vec<JsonValue>> {
	match v {
		JsonValue::Array(a) => Some(a),
		_ => None,
	}
}

/// A string value.
pub(crate) fn text(v: &JsonValue) -> Option<&str> {
	match v {
		JsonValue::String(s) => Some(s),
		_ => None,
	}
}

/// A line or column value: a non-negative integer.
pub(crate) fn position(v: &JsonValue) -> Option<u32> {
	match v {
		JsonValue::Number(n) => crate::numconv::json_u32(*n),
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

#[cfg(all(test, feature = "tools"))]
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

#[cfg(test)]
mod envelope_tests {
	use super::*;

	#[test]
	fn a_document_after_many_bracketed_log_lines_is_found() {
		let mut out = "[INFO] progress\n".repeat(500);
		out.push_str(r#"{"version":"2.1.0","runs":[]}"#);
		let found = find_envelope(out.as_bytes(), |v| member(v, "runs").map(|_| vec![()]));
		assert_eq!(found, Some(vec![()]));
	}

	#[test]
	fn braces_that_never_close_cost_a_bounded_scan() {
		let out = "{ ".repeat(200_000);
		assert!(spans(&out).count() < 400);
	}

	#[test]
	fn a_document_nested_past_the_limit_is_not_parsed() {
		let deep = format!("{}{}", "[".repeat(100_000), "]".repeat(100_000));
		assert_eq!(find_envelope(deep.as_bytes(), |_| Some(vec![()])), None);
		assert!(!cut(deep.as_bytes()));
		let fine = format!("{}{}", "[".repeat(MAX_DEPTH), "]".repeat(MAX_DEPTH));
		assert_eq!(find_envelope(fine.as_bytes(), |_| Some(vec![()])), Some(vec![()]));
	}

	#[test]
	fn a_value_that_runs_to_the_end_is_cut() {
		for out in [
			&br#"{"version":"2.1.0","runs":[{"results":[{"message""#[..],
			b"scanning...\n[1, 2",
			br#"{"a":"unterminated"#,
			b"{} then [",
		] {
			assert!(cut(out), "{}", String::from_utf8_lossy(out));
		}
		for out in [
			&br#"{"version":"2.1.0","runs":[]}"#[..],
			b"[INFO] done\n",
			b"Formatted {count} files [ok]",
			b"{} noise",
			b"",
		] {
			assert!(!cut(out), "{}", String::from_utf8_lossy(out));
		}
	}

	fn tagged(v: &JsonValue) -> Option<Vec<u32>> {
		member(v, "tag").and_then(position).map(|t| vec![t])
	}

	#[test]
	fn finds_the_envelope_behind_documents_that_are_not_one() {
		let out = find_envelope(br#"log {"level":"info"} [1,2] then {"tag":7} done"#, tagged);
		assert_eq!(out, Some(vec![7]));
	}

	#[test]
	fn no_envelope_is_none_and_a_document_is_still_seen() {
		begin_parse();
		assert_eq!(find_envelope(br#"{"level":"info"}"#, tagged), None);
		assert!(DOCUMENT_SEEN.with(Cell::get));
		begin_parse();
		assert_eq!(find_envelope(b"no json at all", tagged), None);
		assert!(!DOCUMENT_SEEN.with(Cell::get));
	}
}
