//! JUnit XML: a `<testsuites>` or `<testsuite>` root whose `<testcase>`
//! elements may hold a `<failure>` or an `<error>`. Each of those is one
//! finding: the test case's `name` is the code, its `message` attribute (or
//! the first line of its text) the message.
//!
//! A finding's position is the test case's `file`, `line` and `column`
//! attributes, or, for a linter that writes the location there, a `classname`
//! of the form `path:line[:col]` (`golangci-lint`'s JUnit output). Any other
//! `classname` — a test class — prefixes the message, so two failures of one
//! test name in two classes stay apart. `source` is left to the core, which names the tool that
//! ran; a test class is not one. The level is the element's `type` attribute
//! where it is a level word.
use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::response::Response;
use crate::severity::{self, Level};

use super::xml::{attr, Doc, Token, Tokenizer};

const LEVELS: &[Level] = &[
	Level("error", severity::ERROR),
	Level("warning", severity::WARNING),
	Level("info", severity::INFO),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "junit-xml",
	description: "JUnit XML, as `golangci-lint run --output.junit-xml.path=stdout` and test runners print it: \
        one finding per `failure` or `error` of a test case, whose name is the code. Recognized by a \
        `<testsuites>` or `<testsuite>` root, failures or not.",
	url: "",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	super::each_stream(DESCRIPTOR.name, stdout, stderr, |s| {
		match document(&String::from_utf8_lossy(s)) {
			Doc::Whole(diags) => Some(diags),
			_ => None,
		}
	})
}

/// The test case a failure belongs to.
#[derive(Default)]
struct Case {
	name: Option<String>,
	classname: Option<String>,
	file: Option<String>,
	line: Option<u32>,
	column: Option<u32>,
}

/// A failure or error being read.
struct Open {
	message: Option<String>,
	level: Option<u8>,
	text: String,
}

/// Whether `text` holds a JUnit document that cannot be read whole.
pub(crate) fn broken(text: &str) -> bool {
	document(text) == Doc::Broken
}

/// The JUnit document in `text`, whose root opens a line.
fn document(text: &str) -> Doc {
	super::xml::document(text, "testsuite", from)
}

fn from(text: &str) -> Doc {
	let mut tokens = Tokenizer::new(text);
	let root = match tokens.next() {
		Some(Token::Start {
			name,
			self_closing: true,
			..
		}) if name == "testsuites" || name == "testsuite" => return Doc::Whole(Vec::new()),
		Some(Token::Start { name, .. }) if name == "testsuites" || name == "testsuite" => name,
		Some(_) => return Doc::Not,
		None => return Doc::Broken,
	};
	// A testsuite root may hold testsuites of its own.
	let mut depth = 1;
	let mut case = Case::default();
	let mut open: Option<Open> = None;
	let mut out = Vec::new();
	for token in tokens {
		match token {
			Token::Start {
				name: "testcase",
				attrs,
				..
			} => {
				case = Case {
					name: attr(&attrs, "name").filter(|s| !s.is_empty()).map(str::to_string),
					classname: attr(&attrs, "classname").filter(|s| !s.is_empty()).map(str::to_string),
					file: attr(&attrs, "file").and_then(crate::diagnostic::file_field),
					line: attr(&attrs, "line").and_then(|v| v.trim().parse().ok()),
					column: attr(&attrs, "column").and_then(|v| v.trim().parse().ok()),
				};
			}
			Token::Start {
				name: "failure" | "error",
				attrs,
				self_closing,
			} => {
				let failure = Open {
					message: attr(&attrs, "message")
						.filter(|s| !s.trim().is_empty())
						.map(str::to_string),
					level: attr(&attrs, "type").and_then(|t| severity::of(LEVELS, t)),
					text: String::new(),
				};
				if self_closing {
					out.extend(finding(&case, failure));
				} else {
					open = Some(failure);
				}
			}
			Token::Text(t) => {
				if let Some(o) = open.as_mut() {
					o.text.push_str(&t);
				}
			}
			Token::End {
				name: "failure" | "error",
			} => {
				if let Some(o) = open.take() {
					out.extend(finding(&case, o));
				}
			}
			Token::Start {
				name,
				self_closing: false,
				..
			} if name == root => depth += 1,
			Token::End { name } if name == root => {
				depth -= 1;
				if depth == 0 {
					return Doc::Whole(out);
				}
			}
			_ => {}
		}
	}
	Doc::Broken
}

fn finding(case: &Case, failure: Open) -> Option<RawDiagnostic> {
	let text_line = failure
		.text
		.lines()
		.map(str::trim)
		.find(|l| !l.is_empty())
		.map(str::to_string);
	let mut message = failure.message.or(text_line).or_else(|| case.name.clone())?;
	let mut d = RawDiagnostic {
		severity: failure.level,
		code: case.name.clone(),
		file: case.file.clone(),
		row: case.line,
		col: case.column,
		..RawDiagnostic::default()
	};
	match case.classname.as_deref() {
		Some(class) if d.file.is_none() && d.row.is_none() => match classname_location(class) {
			Some((file, row, col)) => {
				d.file = crate::diagnostic::file_field(file);
				d.row = Some(row);
				d.col = col;
			}
			None => message = format!("{class}: {message}"),
		},
		Some(class) => message = format!("{class}: {message}"),
		None => {}
	}
	d.message = message;
	Some(d)
}

/// `path:line` or `path:line:col`.
fn classname_location(class: &str) -> Option<(&str, u32, Option<u32>)> {
	if let Some((file, row, col)) = crate::location::file_row_col(class) {
		return (!file.is_empty()).then_some((file, row, Some(col)));
	}
	let (file, row) = crate::location::file_row(class)?;
	(!file.is_empty()).then_some((file, row, None))
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const GOLANGCI: &[u8] = br#"<testsuites>
  <testsuite name="pkg/a.go" tests="1" errors="0" failures="1">
    <testcase name="errcheck" classname="pkg/a.go:12:5">
      <failure message="pkg/a.go:12:5: Error return value is not checked" type="error"><![CDATA[error: Error return value is not checked (errcheck)
Category: errcheck
File: pkg/a.go
Line: 12]]></failure>
    </testcase>
  </testsuite>
</testsuites>
"#;

	pub(super) const PYTEST: &[u8] = br#"<?xml version="1.0" encoding="utf-8"?><testsuites><testsuite name="pytest" errors="1" failures="1" tests="3"><testcase classname="tests.test_a" name="test_ok" time="0.001" /><testcase classname="tests.test_a" name="test_sum" file="tests/test_a.py" line="7" time="0.002"><failure message="assert 3 == 4">def test_sum():
&gt;       assert 1 + 2 == 4</failure></testcase><testcase classname="tests.test_b" name="test_io" time="0.1"><error message="">OSError: disk full
at open()</error></testcase><testcase classname="tests.test_b" name="test_skip"><skipped message="later"/></testcase></testsuite></testsuites>"#;

	#[test]
	fn a_linter_writes_its_location_in_the_classname() {
		let r = parse(GOLANGCI, b"", 1);
		assert!(r.recognized);
		let d = &r.diagnostics[0];
		assert_eq!((d.file.as_deref(), d.row, d.col), (Some("pkg/a.go"), Some(12), Some(5)));
		assert_eq!(d.code.as_deref(), Some("errcheck"));
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.message, "pkg/a.go:12:5: Error return value is not checked");
	}

	#[test]
	fn a_test_runner_names_failures_and_errors() {
		let r = parse(PYTEST, b"", 1);
		assert_eq!(r.diagnostics.len(), 2);
		let (f, e) = (&r.diagnostics[0], &r.diagnostics[1]);
		assert_eq!((f.file.as_deref(), f.row), (Some("tests/test_a.py"), Some(7)));
		assert_eq!(f.code.as_deref(), Some("test_sum"));
		assert_eq!(f.message, "tests.test_a: assert 3 == 4");
		assert_eq!(f.severity, None);
		assert_eq!((e.file.as_deref(), e.code.as_deref()), (None, Some("test_io")));
		assert_eq!(e.message, "tests.test_b: OSError: disk full");
	}

	#[test]
	fn a_passing_suite_is_recognized_and_empty() {
		for out in [
			&br#"<testsuites tests="0"/>"#[..],
			br#"<testsuite name="x" tests="1"><testcase name="t"/></testsuite>"#,
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
	fn a_truncated_suite_is_not_recognized() {
		let cut = &PYTEST[..PYTEST.len() - 150];
		assert!(!parse(cut, b"", 1).recognized);
		assert!(broken(&String::from_utf8_lossy(cut)));
	}

	#[test]
	fn a_failure_left_open_breaks_the_document() {
		let out = br#"<testsuite><testcase name="t"><failure message="lost"></testsuite>"#;
		assert!(!parse(out, b"", 0).recognized);
		assert!(broken(&String::from_utf8_lossy(out)));
	}

	#[test]
	fn a_whole_inner_suite_does_not_stand_for_a_cut_off_document() {
		let cut = br#"<testsuites>
  <testsuite name="a"><testcase name="t1"/></testsuite>
  <testsuite name="b"><testcase name="t2"><failure message="two"/>"#;
		assert!(!parse(cut, b"", 1).recognized);
		assert!(broken(&String::from_utf8_lossy(cut)));
	}

	#[test]
	fn a_document_quoted_in_a_failure_is_part_of_it() {
		let out = br#"<testsuite name="x">
<testcase name="t"><failure message="lint failed"><![CDATA[
<checkstyle/>
]]></failure></testcase>
</testsuite>"#;
		let r = crate::fallback::sniff(out, b"", 1);
		assert_eq!((r.format.as_str(), r.diagnostics.len()), ("junit-xml", 1));
		assert!(!crate::format::checkstyle_xml::parse(out, b"", 1).recognized);
	}

	#[test]
	fn a_suite_of_suites_keeps_every_failure() {
		let nested = br#"<testsuite name="all">
<testsuite name="a"><testcase name="t1"><failure message="one"/></testcase></testsuite>
<testsuite name="b"><testcase name="t2"><failure message="two"/></testcase></testsuite>
</testsuite>"#;
		let r = parse(nested, b"", 1);
		assert!(r.recognized);
		let codes: Vec<_> = r.diagnostics.iter().map(|d| d.code.as_deref()).collect();
		assert_eq!(codes, [Some("t1"), Some("t2")]);
	}

	#[test]
	fn other_xml_is_no_suite() {
		for out in [&b"<checkstyle/>"[..], b"<testsuitex/>", b"plain"] {
			assert!(!parse(out, b"", 1).recognized, "{}", String::from_utf8_lossy(out));
		}
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: tests::GOLANGCI,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: tests::PYTEST,
		stderr: b"",
		exit: 1,
	},
];
