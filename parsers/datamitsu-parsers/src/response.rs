//! What `parse` answers (response ABI 2).
//!
//! An answer says whether the parser understood the output at all, which a bare
//! list of findings cannot: `recognized: false` is "nothing here is in my
//! format", `recognized: true` with no diagnostics is "my format, and it found
//! nothing". The core falls back to the embedded sniffer only on the first.

use crate::diagnostic::{json_string, to_json_array, RawDiagnostic};

/// One parse's answer.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct Response {
	/// Whether the parser found its format in the output.
	pub recognized: bool,
	/// The format key a format parser or the sniffer read, or the tool name
	/// for a tool parser; empty when no parser answered.
	pub format: String,
	pub diagnostics: Vec<RawDiagnostic>,
}

impl Response {
	/// An answer that recognized `format`.
	pub fn recognized(format: &str, diagnostics: Vec<RawDiagnostic>) -> Self {
		Response {
			recognized: true,
			format: format.to_string(),
			diagnostics,
		}
	}

	/// An answer that found nothing it understands.
	pub fn unrecognized(format: &str) -> Self {
		Response {
			recognized: false,
			format: format.to_string(),
			diagnostics: Vec::new(),
		}
	}

	/// The answer as the host reads it.
	pub fn to_json(&self) -> String {
		format!(
			r#"{{"recognized":{},"format":{},"diagnostics":{}}}"#,
			self.recognized,
			json_string(&self.format),
			to_json_array(&self.diagnostics),
		)
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn serializes_the_three_fields() {
		let r = Response::recognized(
			"sarif",
			vec![RawDiagnostic {
				message: "m".to_string(),
				..RawDiagnostic::default()
			}],
		);
		assert_eq!(
			r.to_json(),
			r#"{"recognized":true,"format":"sarif","diagnostics":[{"message":"m"}]}"#
		);
		assert_eq!(
			Response::unrecognized("gcc").to_json(),
			r#"{"recognized":false,"format":"gcc","diagnostics":[]}"#
		);
	}
}
