//! perlimports — A command line utility for cleaning up imports in your Perl code.
//! Ported from the none-ls diagnostics/perlimports builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "perlimports",
	description: "A command line utility for cleaning up imports in your Perl code",
	url: "https://metacpan.org/dist/App-perlimports/view/script/perlimports",
	operations: &[Operation {
		mode: "lint",
		// to_stdin=true, $FILENAME passed via --filename
		args: &["--lint", "--read-stdin", "--filename", "{file}"],
		stdin: true,
	}],
};

// from_stderr = true
pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Lua pattern: %((.*)%) at (.*) line (%d+)
//   "(" <message> ")" " at " <filename> " line " <row>
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Find " line " and parse trailing digits as the row.
	let line_idx = line.rfind(" line ")?;
	let after_line = &line[line_idx + " line ".len()..];
	let digits: String = after_line.chars().take_while(|c| c.is_ascii_digit()).collect();
	if digits.is_empty() {
		return None;
	}
	let row: u32 = digits.parse().ok()?;

	// Before " line " must contain ") at " separating message ")" from filename.
	let head = &line[..line_idx];
	let at_idx = head.rfind(") at ")?;
	let open_idx = head.find('(')?;
	if open_idx >= at_idx {
		return None;
	}
	// message is between the first "(" and the ") at "
	let message = head[open_idx + 1..at_idx].to_string();

	Some(RawDiagnostic {
		message,
		row: Some(row),
		severity: Some(severity::ERROR),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_perlimports_lint_line() {
		let stderr = b"(Carp is unused) at lib/Foo.pm line 12\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].message, "Carp is unused");
		assert_eq!(diags[0].row, Some(12));
		assert_eq!(diags[0].severity, Some(severity::ERROR));
	}

	#[test]
	fn ignores_non_matching_lines() {
		let diags = parse(b"", b"some unrelated output\n", 0);
		assert!(diags.is_empty());
	}
}
