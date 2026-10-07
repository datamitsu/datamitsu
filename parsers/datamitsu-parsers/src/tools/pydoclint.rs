//! pydoclint — Python docstring linter checking docstring sections against
//! signatures. Ported from the none-ls diagnostics/pydoclint builtin.
//!
//! pydoclint prints no level, so its findings carry none.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "pydoclint",
	description: "Pydoclint is a Python docstring linter to check whether a docstring's sections (arguments, returns, raises, ...) match the function signature or function implementation. To see all violation codes go to [pydoclint](https://jsh9.github.io/pydoclint/violation_codes.html)",
	url: "https://github.com/jsh9/pydoclint",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
	// generator_opts: to_temp_file + from_stderr; diagnostics read from stderr.
	operations: &[Operation {
		mode: "lint",
		args: &["--show-filenames-in-every-violation-message=true", "-q", "{file}"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Upstream Lua pattern (file prefix escaped): `<path>:(%d+): (DOC%d+: .*)`
// e.g. `rel/path/to/file.py:42: DOC101: Docstring contains fewer arguments...`
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Find the trailing `:<row>: DOC...` segment. The DOC marker disambiguates
	// from any colons inside the path.
	let doc_idx = line.find(": DOC")?;
	let (head, rest) = line.split_at(doc_idx);
	// head ends with `:<row>` ; rest starts with `: ` then `DOC<digits>: ...`
	let row_str = head.rsplit(':').next()?;
	if row_str.is_empty() || !row_str.bytes().all(|b| b.is_ascii_digit()) {
		return None;
	}
	let row: u32 = row_str.parse().ok()?;

	let (code, message) = rest.strip_prefix(": ")?.split_once(": ")?;
	if code.len() <= 3 || !code.starts_with("DOC") || !code[3..].bytes().all(|b| b.is_ascii_digit()) {
		return None;
	}

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		code: Some(code.to_string()),
		file: head
			.rsplit_once(':')
			.and_then(|(file, _)| crate::diagnostic::file_field(file)),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_violation() {
		let diags = parse(b"", SAMPLES[0].stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(42));
		assert_eq!(diags[0].code.as_deref(), Some("DOC101"));
		assert_eq!(
			diags[0].message,
			"Docstring contains fewer arguments than in function signature."
		);
		assert_eq!(diags[0].col, None);
	}

	#[test]
	fn never_sets_a_severity() {
		for s in SAMPLES {
			let diags = parse(s.stdout, s.stderr, s.exit);
			assert!(!diags.is_empty());
			assert!(diags.iter().all(|d| d.severity.is_none()), "{diags:?}");
		}
	}

	#[test]
	fn ignores_non_matching_lines() {
		let stderr = b"Loading config...\nsrc/a.py:7: DOC201: does not have a return section\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(7));
		assert_eq!(diags[0].code.as_deref(), Some("DOC201"));
		assert_eq!(diags[0].message, "does not have a return section");
	}

	#[test]
	fn each_finding_names_its_file() {
		let stderr = b"src/a.py:1: DOC101: first\nsrc/pkg/b.py:2: DOC201: second\n";
		let files: Vec<_> = parse(b"", stderr, 1).into_iter().map(|d| d.file).collect();
		assert_eq!(files, [Some("src/a.py".to_string()), Some("src/pkg/b.py".to_string())]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: b"src/foo.py:42: DOC101: Docstring contains fewer arguments than in function signature.\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"src/a.py:7: DOC201: Function `f` does not have a return section in docstring \n\
src/a.py:12: DOC501: Function `g` has raise statements, but the docstring does not have a \"Raises\" section \n",
		exit: 1,
	},
];
