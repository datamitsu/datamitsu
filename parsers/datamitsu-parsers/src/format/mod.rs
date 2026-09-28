//! Parsers for the standard formats many tools print on request — SARIF,
//! Code Climate, ESLint's JSON, GitHub and Azure log commands, the compiler
//! lines, Checkstyle and JUnit XML — callable by key like a tool parser, and the
//! pieces the sniffer (`crate::fallback`) tries in turn.
//!
//! A structured format is recognized by its envelope, whatever its finding
//! count: a SARIF log with no result, an ESLint report whose files have no
//! message, a `<checkstyle/>` with no file are recognized and clean. A bare `[]`
//! or `{}` has no envelope and is not. A line format is recognized when at least
//! one line matches. Each parser reads stdout, and stderr when stdout does not
//! hold its format.

pub mod azure_logissue;
pub mod checkstyle_xml;
pub mod codeclimate;
pub mod eslint_json;
#[cfg(test)]
pub(crate) mod fixtures;
pub mod gcc;
pub mod github_annotations;
pub mod json;
pub mod junit_xml;
mod lines;
pub mod msvc;
pub mod sarif;
mod xml;

use crate::capabilities::ToolCapability;
use crate::response::Response;

/// The format parsers' descriptors, the sniffer's among them.
pub(crate) const DESCRIPTORS: &[&ToolCapability] = &[
	&sarif::DESCRIPTOR,
	&codeclimate::DESCRIPTOR,
	&eslint_json::DESCRIPTOR,
	&json::DESCRIPTOR,
	&checkstyle_xml::DESCRIPTOR,
	&junit_xml::DESCRIPTOR,
	&github_annotations::DESCRIPTOR,
	&azure_logissue::DESCRIPTOR,
	&msvc::DESCRIPTOR,
	&gcc::DESCRIPTOR,
	&crate::fallback::DESCRIPTOR,
];

/// A format parser, the sniffer's candidates in the order it tries them.
pub(crate) type Parser = fn(&[u8], &[u8], i32) -> Response;

/// Every format parser by key, in the order the sniffer tries them: the
/// structured formats, which an envelope identifies, before the line formats,
/// which one matching line does.
pub(crate) const PARSERS: &[(&str, Parser)] = &[
	("sarif", sarif::parse),
	("codeclimate", codeclimate::parse),
	("eslint-json", eslint_json::parse),
	("json", json::parse),
	("checkstyle-xml", checkstyle_xml::parse),
	("junit-xml", junit_xml::parse),
	("github-annotations", github_annotations::parse),
	("azure-logissue", azure_logissue::parse),
	("msvc", msvc::parse),
	("gcc", gcc::parse),
];

/// Run the format parser `key` a configuration declared, or the sniffer for
/// `fallback`; `None` for a key that is neither.
///
/// A declared format answers as `crate::fallback::declared` settles it. The
/// sniffer recognizes only what one of the formats matched, exit code or not:
/// it guesses, and a guess needs evidence.
pub fn dispatch(key: &str, stdout: &[u8], stderr: &[u8], exit_code: i32) -> Option<Response> {
	if key == crate::fallback::DESCRIPTOR.name {
		return Some(crate::fallback::sniff(stdout, stderr, exit_code));
	}
	let (_, parse) = PARSERS.iter().find(|(name, _)| *name == key)?;
	let answer = parse(stdout, stderr, exit_code);
	Some(crate::fallback::declared(
		key,
		answer.diagnostics,
		answer.recognized,
		stdout,
		stderr,
		exit_code,
	))
}

/// Whether `stream` holds a document that begins and cannot be read whole: a
/// JSON value cut off before it closes, or a Checkstyle or JUnit document cut
/// off or holding a tag that cannot be read. What it held cannot all be known.
pub(crate) fn unfinished(stream: &[u8]) -> bool {
	if crate::json_diag::cut(stream) {
		return true;
	}
	let text = String::from_utf8_lossy(stream);
	checkstyle_xml::broken(&text) || junit_xml::broken(&text)
}

/// A format parser's contract samples (`SAMPLES`), by key.
#[cfg(test)]
pub(crate) fn samples(key: &str) -> Option<&'static [crate::contract::Sample]> {
	Some(match key {
		"sarif" => sarif::SAMPLES,
		"codeclimate" => codeclimate::SAMPLES,
		"eslint-json" => eslint_json::SAMPLES,
		"json" => json::SAMPLES,
		"checkstyle-xml" => checkstyle_xml::SAMPLES,
		"junit-xml" => junit_xml::SAMPLES,
		"github-annotations" => github_annotations::SAMPLES,
		"azure-logissue" => azure_logissue::SAMPLES,
		"msvc" => msvc::SAMPLES,
		"gcc" => gcc::SAMPLES,
		"fallback" => crate::fallback::SAMPLES,
		_ => return None,
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn every_parser_is_described_and_dispatched_under_its_key() {
		for (key, _) in PARSERS {
			assert!(DESCRIPTORS.iter().any(|d| d.name == *key), "{key} is not described");
			assert!(dispatch(key, b"", b"", 0).is_some(), "{key} does not dispatch");
		}
		for d in DESCRIPTORS {
			assert_eq!(d.kind, "format", "{}", d.name);
			assert!(dispatch(d.name, b"", b"", 0).is_some(), "{} does not dispatch", d.name);
		}
		assert!(dispatch("eslint", b"", b"", 0).is_none());
	}

	#[test]
	fn a_declared_format_takes_a_clean_exit_for_recognition_and_the_sniffer_does_not() {
		for (key, _) in PARSERS {
			let clean = dispatch(key, b"All checks passed!", b"", 0).expect("a format");
			assert!(clean.recognized && clean.diagnostics.is_empty(), "{key}");
			let failed = dispatch(key, b"All checks passed!", b"", 1).expect("a format");
			assert!(!failed.recognized, "{key}");
		}
		assert!(
			!dispatch("fallback", b"All checks passed!", b"", 0)
				.expect("the sniffer")
				.recognized
		);
	}

	#[test]
	fn a_declared_format_leaves_another_formats_findings_to_the_fallback() {
		let sarif = br#"{"version":"2.1.0","runs":[{"results":[{"level":"error","message":{"text":"m"}}]}]}"#;
		let r = dispatch("gcc", sarif, b"", 0).expect("a format");
		assert!(!r.recognized, "a clean exit must not hide another format's findings");
	}

	#[test]
	fn a_document_cut_off_is_not_recognized_whatever_the_exit_code() {
		let sarif = br#"{"version":"2.1.0","runs":[{"results":[{"level":"error","message":{"text":"m"}}]}]}"#;
		let checkstyle = br#"<checkstyle version="4.3"><file name="a"><error line="1" message="m"/></file></checkstyle>"#;
		let junit = br#"<testsuites><testsuite name="s"><testcase name="t"><failure message="m"/></testcase></testsuite></testsuites>"#;
		for (key, whole) in [
			("sarif", &sarif[..]),
			(
				"codeclimate",
				br#"[{"check_name":"c","description":"d","location":{"path":"a","lines":{"begin":1}}}]"#,
			),
			(
				"eslint-json",
				br#"[{"filePath":"/a.js","messages":[{"message":"m","severity":2}]}]"#,
			),
			("json", br#"[{"message":"m","line":2}]"#),
			("checkstyle-xml", checkstyle),
			("junit-xml", junit),
		] {
			let cut = &whole[..whole.len() - 12];
			for key in [key, "gcc"] {
				for (out, err) in [(cut, &b""[..]), (&b""[..], cut)] {
					let r = dispatch(key, out, err, 0).expect("a format");
					assert!(!r.recognized, "{key} recognized a cut-off document at exit 0");
				}
			}
		}
	}

	#[test]
	fn a_format_answers_with_its_key() {
		for (key, parse) in PARSERS {
			let r = parse(b"nothing to see", b"", 1);
			assert!(!r.recognized, "{key} recognized prose");
			assert_eq!(r.format, *key);
		}
	}
}
