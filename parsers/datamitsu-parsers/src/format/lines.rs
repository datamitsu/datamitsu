//! What the line formats share: the lines of both streams, without the ANSI
//! control sequences the core already strips (again here, defensively, for a
//! module run on its own), and the trailing rule code many tools append.

/// The lines of stdout, then of stderr, each without its control sequences.
pub(crate) fn of(stdout: &[u8], stderr: &[u8]) -> Vec<String> {
	let mut out = Vec::new();
	for stream in [stdout, stderr] {
		let text = String::from_utf8_lossy(stream);
		out.extend(text.lines().map(strip_ansi));
	}
	out
}

/// `s` without ANSI control sequences: CSI sequences (`ESC [` … final byte),
/// OSC strings (`ESC ]` … BEL or `ESC \`) and two-byte escapes.
pub(crate) fn strip_ansi(s: &str) -> String {
	if !s.contains('\u{1b}') {
		return s.to_string();
	}
	let mut out = String::with_capacity(s.len());
	let mut chars = s.chars().peekable();
	while let Some(c) = chars.next() {
		if c != '\u{1b}' {
			out.push(c);
			continue;
		}
		match chars.next() {
			Some('[') => {
				for c in chars.by_ref() {
					if ('\u{40}'..='\u{7e}').contains(&c) {
						break;
					}
				}
			}
			Some(']') => {
				while let Some(c) = chars.next() {
					if c == '\u{7}' || (c == '\u{1b}' && chars.next_if_eq(&'\\').is_some()) {
						break;
					}
				}
			}
			_ => {}
		}
	}
	out
}

/// The rule a message ends with in brackets or parentheses — `[-Wunused]`,
/// `[SC2086]`, `(errcheck)` — when it is one token. A parenthetical with a
/// space in it is prose, not a rule. `-Werror=<rule>` only says the rule was
/// made an error, so it names `-W<rule>`.
pub(crate) fn trailing_code(message: &str) -> Option<String> {
	let message = message.trim_end();
	let (open, close) = match message.chars().last()? {
		']' => ('[', ']'),
		')' => ('(', ')'),
		_ => return None,
	};
	let start = message.rfind(open)?;
	let code = &message[start + 1..message.len() - close.len_utf8()];
	if code.is_empty() || code.contains(|c: char| c.is_whitespace() || "[]()".contains(c)) {
		return None;
	}
	Some(match code.strip_prefix("-Werror=") {
		Some(rule) => format!("-W{rule}"),
		None => code.to_string(),
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn strips_colour_and_hyperlinks() {
		assert_eq!(strip_ansi("\u{1b}[1;31merror\u{1b}[0m: x"), "error: x");
		assert_eq!(
			strip_ansi("\u{1b}]8;;file:///a\u{1b}\\a.c\u{1b}]8;;\u{7}:1:2"),
			"a.c:1:2"
		);
		assert_eq!(strip_ansi("plain"), "plain");
	}

	#[test]
	fn reads_both_streams_in_order() {
		assert_eq!(of(b"a\nb\n", b"c"), vec!["a", "b", "c"]);
	}

	#[test]
	fn a_trailing_code_is_one_token() {
		assert_eq!(
			trailing_code("unused variable 'x' [-Wunused-variable]").as_deref(),
			Some("-Wunused-variable")
		);
		assert_eq!(
			trailing_code("unused [-Werror=unused-variable]").as_deref(),
			Some("-Wunused-variable")
		);
		assert_eq!(
			trailing_code("Error return value is not checked (errcheck)").as_deref(),
			Some("errcheck")
		);
		assert_eq!(trailing_code("Double quote this [SC2086]").as_deref(), Some("SC2086"));
		assert_eq!(trailing_code("'foo' undeclared (first use in this function)"), None);
		assert_eq!(trailing_code("empty ()"), None);
		assert_eq!(trailing_code("no code"), None);
	}
}
