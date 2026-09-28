//! The sniffer: try every format parser in turn and answer with the first that
//! recognizes the output, named by its format.
//!
//! The structured formats go first — SARIF, Code Climate, ESLint's JSON, the
//! plain JSON array, Checkstyle and JUnit XML — because an envelope identifies
//! them; then the line formats, which one matching line does: GitHub and Azure
//! log commands, MSVC, and last the gcc line, the loosest. The core calls it on
//! the module it embeds when a tool's output has no parser that recognized it.
use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::format::{unfinished, PARSERS};
use crate::response::Response;
use crate::severity::{self, Level};

/// Every level word a format reads, with the level each maps to: the sniffer
/// answers with the vocabulary of the format it picked.
const LEVELS: &[Level] = &[
	Level("error", severity::ERROR),
	Level("fatal error", severity::ERROR),
	Level("blocker", severity::ERROR),
	Level("critical", severity::ERROR),
	Level("major", severity::ERROR),
	Level("2", severity::ERROR),
	Level("warning", severity::WARNING),
	Level("minor", severity::WARNING),
	Level("1", severity::WARNING),
	Level("note", severity::INFO),
	Level("notice", severity::INFO),
	Level("info", severity::INFO),
	Level("information", severity::INFO),
	Level("hint", severity::HINT),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "fallback",
	description: "Tries every format parser — sarif, codeclimate, eslint-json, json, checkstyle-xml, junit-xml, \
        github-annotations, azure-logissue, msvc, gcc — in that order and answers with the first that \
        recognizes the output, named by its format. The core runs the build of it embedded in the binary \
        when a tool's output has no parser that recognized it; `devtools parsers sniff` shows its choice.",
	url: "",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

/// The answer of the first format that recognizes the output; not recognized
/// when none does. Partial when either stream holds a document that cannot be
/// read whole, whatever else was recognized.
pub fn sniff(stdout: &[u8], stderr: &[u8], exit_code: i32) -> Response {
	first(stdout, stderr, exit_code).partial_if(unfinished(stdout) || unfinished(stderr))
}

fn first(stdout: &[u8], stderr: &[u8], exit_code: i32) -> Response {
	PARSERS
		.iter()
		.map(|(_, parse)| parse(stdout, stderr, exit_code))
		.find(|r| r.recognized)
		.unwrap_or_else(|| Response::unrecognized(DESCRIPTOR.name))
}

/// The answer of a declared parser, a tool's or a format's, that read
/// `diagnostics` out of the output and, with `own`, found its own format in it;
/// `searched` says it looked for a JSON or XML document in a stream that holds
/// text.
///
/// A parser that found something recognized the output. One that found
/// nothing did not when the output holds a document it cannot read whole — a
/// JSON value or an XML document cut off, or malformed — which may have held
/// findings, nor when it holds findings in a standard format: the tool printed
/// another format than the parser reads, and the core's fallback is what
/// reads it. Either way the answer is partial when such a document is there:
/// the findings read may not be all of them. Otherwise it recognized the
/// output when its own format was there, or when the run exited 0 and it did
/// not search text for a document: a clean run may print nothing, or a
/// summary no line format describes, but a parser of a structured format that
/// finds no document where the tool printed something has not read it.
pub(crate) fn declared(
	key: &str,
	diagnostics: Vec<RawDiagnostic>,
	own: bool,
	searched: bool,
	stdout: &[u8],
	stderr: &[u8],
	exit_code: i32,
) -> Response {
	let partial = unfinished(stdout) || unfinished(stderr);
	if !diagnostics.is_empty() {
		return Response::recognized(key, diagnostics).partial_if(partial);
	}
	if partial || !first(stdout, stderr, exit_code).diagnostics.is_empty() {
		return Response::unrecognized(key).partial_if(partial);
	}
	if own || (exit_code == 0 && !searched) {
		return Response::recognized(key, Vec::new());
	}
	Response::unrecognized(key)
}

#[cfg(test)]
mod tests {
	use super::*;

	fn picks(stdout: &[u8], stderr: &[u8]) -> (bool, String, usize) {
		let r = sniff(stdout, stderr, 1);
		(r.recognized, r.format, r.diagnostics.len())
	}

	#[test]
	fn picks_the_format_of_each_shape() {
		let cases: &[(&[u8], &str, usize)] = &[
			(
				br#"{"version":"2.1.0","runs":[{"results":[{"message":{"text":"m"}}]}]}"#,
				"sarif",
				1,
			),
			(
				br#"[{"check_name":"c","description":"d","location":{"path":"a","lines":{"begin":1}}}]"#,
				"codeclimate",
				1,
			),
			(
				br#"[{"filePath":"/a.js","messages":[{"message":"m","severity":2}]}]"#,
				"eslint-json",
				1,
			),
			(br#"[{"message":"m","line":2}]"#, "json", 1),
			(
				br#"<checkstyle><file name="a"><error line="1" message="m"/></file></checkstyle>"#,
				"checkstyle-xml",
				1,
			),
			(
				br#"<testsuites><testcase name="t"><failure message="m"/></testcase></testsuites>"#,
				"junit-xml",
				1,
			),
			(b"::error file=a.py,line=1::m\n", "github-annotations", 1),
			(
				b"##vso[task.logissue type=error;sourcepath=a.py]m\n",
				"azure-logissue",
				1,
			),
			(b"a.ts(1,2): error TS1: m\n", "msvc", 1),
			(b"a.c:1:2: error: m\n", "gcc", 1),
		];
		for (out, format, n) in cases {
			assert_eq!(
				picks(out, b""),
				(true, format.to_string(), *n),
				"{}",
				String::from_utf8_lossy(out)
			);
		}
	}

	#[test]
	fn a_clean_structured_document_is_recognized_and_empty() {
		for (out, format) in [
			(&br#"{"version":"2.1.0","runs":[]}"#[..], "sarif"),
			(br#"[{"filePath":"/a.js","messages":[]}]"#, "eslint-json"),
			(b"<checkstyle version=\"4.3\"></checkstyle>", "checkstyle-xml"),
		] {
			assert_eq!(picks(out, b""), (true, format.to_string(), 0));
		}
	}

	#[test]
	fn nothing_it_knows() {
		for out in [
			&b""[..],
			b"[]",
			b"{}",
			b"  [] noise {} ",
			b"All checks passed!\n",
			b"Found 3 errors in 2 files (checked 10 source files)\n",
			br#"{"level":"error","msg":"::error::not a command","at":"a.c:1:2: error: in a string"}"#,
		] {
			let r = sniff(out, b"", 1);
			assert!(!r.recognized, "{}", String::from_utf8_lossy(out));
			assert_eq!(r.format, "fallback");
		}
	}

	#[test]
	fn reads_stderr_too() {
		assert_eq!(
			picks(b"progress 10%\n", b"src/x.go:3:1: warning: w\n"),
			(true, "gcc".to_string(), 1)
		);
	}

	#[test]
	fn a_declared_parser_that_found_nothing_leaves_standard_findings_to_the_fallback() {
		let sarif = br#"{"version":"2.1.0","runs":[{"results":[{"message":{"text":"m"}}]}]}"#;
		assert!(!declared("gcc", vec![], false, false, sarif, b"", 0).recognized);
		assert!(!declared("hadolint", vec![], true, true, sarif, b"", 1).recognized);
		let clean = br#"{"version":"2.1.0","runs":[]}"#;
		assert!(declared("sarif", vec![], true, true, clean, b"", 1).recognized);
		assert!(declared("gcc", vec![], false, false, b"0 issues.\n", b"", 0).recognized);
		assert!(!declared("gcc", vec![], false, false, b"0 issues.\n", b"", 1).recognized);
		assert!(declared("mypy", vec![], false, false, b"", b"", 0).recognized);
	}

	#[test]
	fn a_structured_parser_that_found_no_document_in_text_did_not_read_it() {
		let prose = b"new-format: finding on a.py\n";
		assert!(!declared("sarif", vec![], false, true, prose, b"", 0).recognized);
		assert!(!declared("hadolint", vec![], false, true, prose, b"", 0).recognized);
		assert!(declared("hadolint", vec![], false, false, b"", b"", 0).recognized);
		assert!(declared("gcc", vec![], false, false, prose, b"", 0).recognized);
	}

	#[test]
	fn what_a_cut_off_document_leaves_is_partial() {
		let cut = br#"[{"message":"m","line":1},{"message":"lost""#;
		let salvaged = vec![RawDiagnostic {
			message: "m".to_string(),
			..RawDiagnostic::default()
		}];
		let r = declared("hadolint", salvaged, true, true, cut, b"", 1);
		assert!(r.recognized && r.partial);
		let clean = br#"{"version":"2.1.0","runs":[]}"#;
		let r = sniff(clean, &cut[..], 0);
		assert!(r.recognized && r.partial, "a clean stream must not hide a cut-off one");
		assert!(sniff(cut, b"", 0).partial);
		assert!(!sniff(clean, b"", 0).partial);
	}

	#[test]
	fn a_declared_parser_does_not_recognize_a_document_cut_off_on_a_clean_exit() {
		let cut = br#"{"version":"2.1.0","runs":[{"results":[{"message":{"text":"m"}}"#;
		assert!(!declared("sarif", vec![], false, true, cut, b"", 0).recognized);
		assert!(declared("sarif", vec![], false, true, cut, b"", 0).partial);
		assert!(!declared("eslint", vec![], true, true, cut, b"", 0).recognized);
		assert!(!declared("gcc", vec![], false, false, b"", b"<checkstyle>\n<file name=\"a\">", 0).recognized);
	}

	#[test]
	fn a_structured_format_wins_over_lines_it_mentions() {
		let out = br#"{"version":"2.1.0","runs":[{"results":[{"message":{"text":"a.c:1:2: error: quoted"}}]}]}"#;
		assert_eq!(picks(out, b"").1, "sarif");
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{"version":"2.1.0","runs":[{"results":[{"level":"warning","ruleId":"R1","message":{"text":"m"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"a.py"},"region":{"startLine":2,"startColumn":3,"endColumn":5}}}]}]}]}"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"a.c:3:10: error: 'y' undeclared\na.c:3:10: note: declared here\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"<checkstyle><file name="a.sh"><error line="1" column="1" severity="info" message="m" source="SC1"/></file></checkstyle>"#,
		stderr: b"",
		exit: 1,
	},
];
