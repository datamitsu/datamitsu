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
    /// 1-based line, if the tool reported one.
    pub row: Option<u32>,
    /// 1-based column, if the tool reported one.
    pub col: Option<u32>,
    /// End line of the span, if any.
    pub end_row: Option<u32>,
    /// End column of the span, if any.
    pub end_col: Option<u32>,
    /// Severity code as emitted (the core maps it to its own scale later).
    pub severity: Option<u8>,
    /// The originating tool/source label, if the tool names one.
    pub source: Option<String>,
    /// A rule/diagnostic code, if any.
    pub code: Option<String>,
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
}
