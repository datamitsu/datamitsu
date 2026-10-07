//! Normalized severity scale shared by parsers: 1=error … 4=hint.
//!
//! This matches LSP `DiagnosticSeverity` (1=Error, 2=Warning, 3=Information,
//! 4=Hint), so a parser emits a stable level the Go core maps 1:1.
//!
//! A parser sets a level only from a token the tool printed: a level word, a
//! numeric level, a `severity` field, or a key that is one (`errors[]`,
//! `warnings[]`). Each tool's descriptor lists those tokens with the level each
//! maps to ([`Level`]), and the parser reads a level through [`of`] from that
//! list, so a level the tool never printed has no way in. A finding without a
//! token has no level: the core decides one from the tool's exit code, the only
//! thing such a tool ever says about seriousness.

pub const ERROR: u8 = 1;
pub const WARNING: u8 = 2;
pub const INFO: u8 = 3;
pub const HINT: u8 = 4;

/// One level token a tool prints, spelled as the parser compares it, and the
/// level on the shared scale it maps to.
pub(crate) struct Level(pub(crate) &'static str, pub(crate) u8);

/// The level `token` maps to in a tool's vocabulary; `None` for a token the
/// vocabulary does not list.
pub(crate) fn of(vocabulary: &[Level], token: &str) -> Option<u8> {
	vocabulary.iter().find(|l| l.0 == token).map(|l| l.1)
}

#[cfg(test)]
mod tests {
	use super::*;

	const VOCABULARY: &[Level] = &[Level("error", ERROR), Level("warning", WARNING)];

	#[test]
	fn a_listed_token_maps_to_its_level() {
		assert_eq!(of(VOCABULARY, "error"), Some(ERROR));
		assert_eq!(of(VOCABULARY, "warning"), Some(WARNING));
	}

	#[test]
	fn an_unlisted_token_has_no_level() {
		assert_eq!(of(VOCABULARY, "Error"), None);
		assert_eq!(of(VOCABULARY, "fatal"), None);
		assert_eq!(of(VOCABULARY, ""), None);
		assert_eq!(of(&[], "error"), None);
	}
}
