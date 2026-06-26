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
	let text = String::from_utf8_lossy(bytes);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};
	let mut out = Vec::new();
	match &value {
		JsonValue::Array(items) => {
			for it in items {
				if let Some(d) = from_obj(it, attrs, sev) {
					out.push(d);
				}
			}
		}
		JsonValue::Object(_) => {
			if let Some(d) = from_obj(&value, attrs, sev) {
				out.push(d);
			}
		}
		_ => {}
	}
	out
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
}
