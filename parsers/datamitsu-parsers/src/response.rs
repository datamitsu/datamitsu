//! What `parse` answers (response ABI 2).
//!
//! An answer says whether the parser understood the output at all, which a bare
//! list of findings cannot: `recognized: false` is "nothing here is in my
//! format", `recognized: true` with no diagnostics is "my format, and it found
//! nothing". The core falls back to the embedded sniffer only on the first.
//! `partial` says the output holds a document the parser could not read whole —
//! cut off, or malformed — so findings may be missing whatever it recognized.

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
	/// Whether the output holds a document that could not be read whole.
	pub partial: bool,
}

impl Response {
	/// An answer that recognized `format`.
	pub fn recognized(format: &str, diagnostics: Vec<RawDiagnostic>) -> Self {
		Response {
			recognized: true,
			format: format.to_string(),
			diagnostics,
			partial: false,
		}
	}

	/// An answer that found nothing it understands.
	pub fn unrecognized(format: &str) -> Self {
		Response {
			recognized: false,
			format: format.to_string(),
			diagnostics: Vec::new(),
			partial: false,
		}
	}

	/// This answer, marked partial when `partial` is.
	pub fn partial_if(mut self, partial: bool) -> Self {
		self.partial |= partial;
		self
	}

	/// The answer as the host reads it; `partial` only when it is set.
	pub fn to_json(&self) -> String {
		format!(
			r#"{{"recognized":{},"format":{},"diagnostics":{}{}}}"#,
			self.recognized,
			json_string(&self.format),
			to_json_array(&self.diagnostics),
			if self.partial { r#","partial":true"# } else { "" },
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
		assert_eq!(
			Response::unrecognized("gcc").partial_if(true).to_json(),
			r#"{"recognized":false,"format":"gcc","diagnostics":[],"partial":true}"#
		);
	}
}
