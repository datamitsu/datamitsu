//! The sniffer: try every format parser in turn and answer with the first that
//! recognizes the output, named by its format.
//!
//! The structured formats go first — SARIF, Code Climate, ESLint's JSON, the
//! plain JSON array, Checkstyle and JUnit XML — because an envelope identifies
//! them; then the line formats, which one matching line does: GitHub and Azure
//! log commands, MSVC, and last the gcc line, the loosest. The core calls it on
//! the module it embeds when a tool's output has no parser that recognized it.
use crate::capabilities::ToolCapability;
use crate::format::PARSERS;
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
/// when none does.
pub fn sniff(stdout: &[u8], stderr: &[u8], exit_code: i32) -> Response {
	PARSERS
		.iter()
		.map(|(_, parse)| parse(stdout, stderr, exit_code))
		.find(|r| r.recognized)
		.unwrap_or_else(|| Response::unrecognized(DESCRIPTOR.name))
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
