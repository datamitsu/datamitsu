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
/// A declared format recognized a run that exited 0 whatever it printed, as a
/// tool parser does (`crate::tools::answer`): a clean run may print nothing, or
/// a summary no format describes, and neither is an unknown shape. The sniffer
/// recognizes only what one of the formats matched, exit code or not: it guesses,
/// and a guess needs evidence.
pub fn dispatch(key: &str, stdout: &[u8], stderr: &[u8], exit_code: i32) -> Option<Response> {
	if key == crate::fallback::DESCRIPTOR.name {
		return Some(crate::fallback::sniff(stdout, stderr, exit_code));
	}
	let (_, parse) = PARSERS.iter().find(|(name, _)| *name == key)?;
	let answer = parse(stdout, stderr, exit_code);
	if !answer.recognized && exit_code == 0 {
		return Some(Response::recognized(key, Vec::new()));
	}
	Some(answer)
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
	fn a_format_answers_with_its_key() {
		for (key, parse) in PARSERS {
			let r = parse(b"nothing to see", b"", 1);
			assert!(!r.recognized, "{key} recognized prose");
			assert_eq!(r.format, *key);
		}
	}
}
