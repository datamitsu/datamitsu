//! Checkstyle XML: a `<checkstyle>` root holding `<file name="…">` elements,
//! each holding `<error line column severity message source/>` findings. The
//! `source` is the rule, and a `link` (tflint writes one) its URL. Many linters print it on request, which is what makes
//! it the most common interchange format after SARIF.
use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::response::Response;
use crate::severity::{self, Level};

use super::xml::{attr, roots, Token, Tokenizer};

const LEVELS: &[Level] = &[
	Level("error", severity::ERROR),
	Level("warning", severity::WARNING),
	Level("info", severity::INFO),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "checkstyle-xml",
	description: "Checkstyle XML, as linters print it on request: `shellcheck -f checkstyle`, \
        `hadolint -f checkstyle`, `tflint -f checkstyle`, `oxlint --format checkstyle`, \
        `phpcs --report=checkstyle`. Recognized by a `<checkstyle>` root, files or not.",
	url: "https://checkstyle.org",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	for stream in [stdout, stderr] {
		if let Some(diags) = document(&String::from_utf8_lossy(stream)) {
			return Response::recognized(DESCRIPTOR.name, diags);
		}
	}
	Response::unrecognized(DESCRIPTOR.name)
}

/// The findings of the Checkstyle document in `text`, which starts at a
/// `<checkstyle>` tag opening a line; `None` when there is none, or it is cut
/// off before its root closes.
fn document(text: &str) -> Option<Vec<RawDiagnostic>> {
	roots(text, "checkstyle").find_map(from)
}

fn from(text: &str) -> Option<Vec<RawDiagnostic>> {
	let mut tokens = Tokenizer::new(text);
	match tokens.next()? {
		Token::Start {
			name: "checkstyle",
			self_closing: true,
			..
		} => return Some(Vec::new()),
		Token::Start { name: "checkstyle", .. } => {}
		_ => return None,
	}
	let mut file: Option<String> = None;
	let mut out = Vec::new();
	for token in tokens {
		match token {
			Token::Start {
				name: "file", attrs, ..
			} => {
				file = attr(&attrs, "name").and_then(crate::diagnostic::file_field);
			}
			Token::End { name: "file" } => file = None,
			Token::Start {
				name: "error", attrs, ..
			} => {
				let Some(message) = attr(&attrs, "message") else {
					continue;
				};
				out.push(RawDiagnostic {
					message: message.to_string(),
					row: attr(&attrs, "line").and_then(|v| v.trim().parse().ok()),
					col: attr(&attrs, "column").and_then(|v| v.trim().parse().ok()),
					severity: attr(&attrs, "severity").and_then(|s| severity::of(LEVELS, s)),
					code: attr(&attrs, "source").filter(|s| !s.is_empty()).map(str::to_string),
					url: attr(&attrs, "link").filter(|s| !s.is_empty()).map(str::to_string),
					file: file.clone(),
					..RawDiagnostic::default()
				});
			}
			Token::End { name: "checkstyle" } => return Some(out),
			_ => {}
		}
	}
	None
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const SHELLCHECK: &[u8] = br#"<?xml version='1.0' encoding='UTF-8'?>
<checkstyle version='4.3'>
<file name='scripts/a b.sh' >
<error line='3' column='6' severity='info' message='Double quote to prevent globbing and word splitting.' source='ShellCheck.SC2086' />
<error line='5' column='1' severity='warning' message='x appears unused. Verify use (or export if used externally).' source='ShellCheck.SC2034' />
</file>
<file name='b.sh' >
<error line='1' column='1' severity='error' message='Tips depend on target shell &amp; &lt;yours&gt; is unknown.' source='ShellCheck.SC2148' />
</file>
</checkstyle>
"#;

	#[test]
	fn reads_files_and_their_errors() {
		let r = parse(SHELLCHECK, b"", 1);
		assert!(r.recognized);
		assert_eq!(r.diagnostics.len(), 3);
		let d = &r.diagnostics[0];
		assert_eq!(d.file.as_deref(), Some("scripts/a b.sh"));
		assert_eq!((d.row, d.col), (Some(3), Some(6)));
		assert_eq!(
			(d.severity, d.code.as_deref()),
			(Some(severity::INFO), Some("ShellCheck.SC2086"))
		);
		assert_eq!(r.diagnostics[2].file.as_deref(), Some("b.sh"));
		assert_eq!(
			r.diagnostics[2].message,
			"Tips depend on target shell & <yours> is unknown."
		);
	}

	#[test]
	fn a_clean_document_is_recognized_and_empty() {
		for out in [
			&br#"<?xml version="1.0" encoding="utf-8"?><checkstyle version="4.3"></checkstyle>"#[..],
			b"<checkstyle/>",
			br#"<checkstyle version="5.0"><file name="a.tf"></file></checkstyle>"#,
		] {
			let r = parse(out, b"", 0);
			assert!(
				r.recognized && r.diagnostics.is_empty(),
				"{}",
				String::from_utf8_lossy(out)
			);
		}
	}

	#[test]
	fn a_truncated_document_is_not_recognized() {
		let cut = &SHELLCHECK[..SHELLCHECK.len() / 2];
		assert!(!parse(cut, b"", 1).recognized);
	}

	#[test]
	fn a_root_quoted_in_other_text_is_no_document() {
		for out in [
			&b"a.xml:1:1: error: unexpected <checkstyle/>\n"[..],
			br#"{"message":"<checkstyle version='4.3'></checkstyle>"}"#,
		] {
			assert!(!parse(out, b"", 1).recognized, "{}", String::from_utf8_lossy(out));
		}
		assert!(parse(b"  <checkstyle/>\n", b"", 0).recognized);
	}

	#[test]
	fn reads_the_document_out_of_noise_on_either_stream() {
		let mut noisy = b"Linting 2 files...\n".to_vec();
		noisy.extend_from_slice(SHELLCHECK);
		assert_eq!(parse(b"", &noisy, 1).diagnostics.len(), 3);
	}

	#[test]
	fn other_xml_is_no_document() {
		for out in [
			&b"<testsuites></testsuites>"[..],
			b"<checkstyles/>",
			b"no xml",
			b"a <checkstyle is a word",
		] {
			assert!(!parse(out, b"", 1).recognized, "{}", String::from_utf8_lossy(out));
		}
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: tests::SHELLCHECK,
	stderr: b"",
	exit: 1,
}];
