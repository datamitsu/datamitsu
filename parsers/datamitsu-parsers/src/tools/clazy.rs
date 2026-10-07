//! clazy — Qt-oriented static code analyzer based on the Clang framework.
//! Ported from the none-ls diagnostics/clazy builtin.
//!
//! clang's diagnostic lines, `<file>:<row>:<col>: <level>: <message> [-W<group>]`,
//! 1-based with no end. The level word is read through the vocabulary; a word it
//! does not list sets no level. The trailing `[-W<group>]` names the check.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "clazy",
	description: "Qt-oriented static code analyzer based on the Clang framework",
	url: "https://github.com/KDE/clazy",
	severities: &[
		Level("error", severity::ERROR),
		Level("fatal error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("note", severity::INFO),
		Level("remark", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--ignore-included-files", "--header-filter=$ROOT/.*", "{file}"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Pattern: [^:]+:(%d+):(%d+): ([%w ]+): (.*)$
	// file:row:col: severity: message
	// Find the first ':' (end of file segment), then parse row/col/severity/message.
	let first_colon = line.find(':')?;
	let rest = &line[first_colon + 1..];

	let c1 = rest.find(':')?;
	let row: u32 = rest[..c1].parse().ok()?;
	let after_row = &rest[c1 + 1..];

	let c2 = after_row.find(':')?;
	let col: u32 = after_row[..c2].parse().ok()?;
	let after_col = &after_row[c2 + 1..];

	// after_col begins with " " then "severity: message"
	let after_col = after_col.strip_prefix(' ')?;
	let c3 = after_col.find(':')?;
	let sev_tok = &after_col[..c3];
	// severity is [%w ]+ — alphanumerics and spaces only (e.g. "fatal error")
	if sev_tok.is_empty() || !sev_tok.chars().all(|c| c.is_alphanumeric() || c == ' ') {
		return None;
	}
	let msg = after_col[c3 + 1..].strip_prefix(' ').unwrap_or(&after_col[c3 + 1..]);
	let (message, code) = split_flag(msg);

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity::of(DESCRIPTOR.severities, sev_tok),
		source: Some("clazy".to_string()),
		code,
		file: crate::diagnostic::file_field(&line[..first_colon]),
		..RawDiagnostic::default()
	})
}

/// Split a trailing ` [-W<group>]` (or `[-Werror,-W<group>]`) off the message;
/// the group is the check's identity.
fn split_flag(msg: &str) -> (&str, Option<String>) {
	let Some(open) = msg.strip_suffix(']').and_then(|m| m.rfind(" [")) else {
		return (msg, None);
	};
	let flags = &msg[open + 2..msg.len() - 1];
	let group = flags
		.rsplit(',')
		.next()
		.and_then(|f| f.strip_prefix("-W"))
		.filter(|g| !g.is_empty() && !g.starts_with("error"));
	match group {
		Some(g) => (msg[..open].trim_end(), Some(g.to_string())),
		None => (msg, None),
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_warning() {
		let stderr = b"/src/main.cpp:42:9: warning: Missing reference in range-for [-Wclazy-range-loop]\n";
		let diags = parse(b"", stderr, 0);
		assert_eq!(diags.len(), 1);
		let d = &diags[0];
		assert_eq!(d.row, Some(42));
		assert_eq!(d.col, Some(9));
		assert_eq!(d.severity, Some(severity::WARNING));
		assert_eq!(d.source.as_deref(), Some("clazy"));
		assert_eq!(d.code.as_deref(), Some("clazy-range-loop"));
		assert_eq!(d.message, "Missing reference in range-for");
	}

	#[test]
	fn a_warning_promoted_by_werror_keeps_its_group() {
		let stderr = b"/src/main.cpp:7:3: error: Use QStringLiteral [-Werror,-Wclazy-qstring-allocations]\n";
		let d = &parse(b"", stderr, 1)[0];
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.code.as_deref(), Some("clazy-qstring-allocations"));
		assert_eq!(d.message, "Use QStringLiteral");
	}

	#[test]
	fn parses_fatal_error_and_note() {
		let stderr =
			b"/src/widget.cpp:1:10: fatal error: 'QWidget' file not found\n/src/widget.cpp:5:3: note: expanded from here\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[0].message, "'QWidget' file not found");
		assert_eq!(diags[0].code, None);
		assert_eq!(diags[1].severity, Some(severity::INFO));
	}

	#[test]
	fn an_unknown_level_token_sets_no_severity() {
		let stderr = b"/src/main.cpp:3:1: ignored: something odd\n";
		let diags = parse(b"", stderr, 0);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].severity, None);
	}

	#[test]
	fn a_bracket_that_is_not_a_flag_stays_in_the_message() {
		let d = parse_line("/src/a.cpp:1:1: warning: index is out of range [0, 3]").unwrap();
		assert_eq!(d.message, "index is out of range [0, 3]");
		assert_eq!(d.code, None);
	}

	#[test]
	fn ignores_unmatched_lines() {
		let stderr = b"some progress output without diagnostics\n";
		assert!(parse(b"", stderr, 0).is_empty());
	}

	#[test]
	fn each_finding_names_its_file() {
		let stderr = b"/src/a.cpp:1:1: warning: first [-Wclazy-x]\n/src/b.h:2:3: note: second\n";
		let files: Vec<_> = parse(b"", stderr, 0).into_iter().map(|d| d.file).collect();
		assert_eq!(files, [Some("/src/a.cpp".to_string()), Some("/src/b.h".to_string())]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: b"/work/src/main.cpp:42:9: warning: Missing reference in range-for with non trivial type (QString) [-Wclazy-range-loop-reference]\n    for (auto s : list) {\n        ^\n/work/src/main.cpp:51:15: warning: Use multi-arg instead [-Wclazy-qstring-arg]\n    auto t = QString(\"%1 %2\").arg(a).arg(b);\n              ^\n2 warnings generated.\n",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"/work/src/widget.cpp:1:10: fatal error: 'QWidget' file not found\n#include <QWidget>\n         ^~~~~~~~~\n/work/src/widget.cpp:5:3: note: expanded from macro 'Q_OBJECT'\n1 error generated.\n",
		exit: 1,
	},
];
