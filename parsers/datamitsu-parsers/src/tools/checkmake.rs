//! checkmake — `make` linter. Ported from the none-ls diagnostics/checkmake builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "checkmake",
	description: "`make` linter.",
	url: "https://github.com/mrtazz/checkmake",
	operations: &[Operation {
		mode: "lint",
		args: &["--format='{{.LineNumber}}:{{.Rule}}:{{.Violation}}\n'", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stdout).lines().filter_map(parse_line).collect()
}

// Pattern: (%d+):(%w+):(.+)  groups = row, code, message
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let (row_str, rest) = line.split_once(':')?;
	let row: u32 = row_str.trim().parse().ok()?;

	let (code, message) = rest.split_once(':')?;
	// %w+ requires a non-empty alphanumeric rule token.
	if code.is_empty() || !code.chars().all(|c| c.is_ascii_alphanumeric() || c == '_') {
		return None;
	}
	if message.is_empty() {
		return None;
	}

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		code: Some(code.to_string()),
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_violations() {
		let out = b"4:maxbodylength:Target body exceeds allowed length\n\
                    1:minphony:Missing required phony target \"all\"\n";
		let diags = parse(out, b"", 1);
		assert_eq!(diags.len(), 2);
		assert_eq!(diags[0].row, Some(4));
		assert_eq!(diags[0].code.as_deref(), Some("maxbodylength"));
		assert_eq!(diags[0].message, "Target body exceeds allowed length");
		assert_eq!(diags[1].row, Some(1));
		assert_eq!(diags[1].code.as_deref(), Some("minphony"));
	}

	#[test]
	fn message_with_colons_preserved() {
		let diags = parse(b"7:phonydeclared:Phony target: all not declared\n", b"", 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].message, "Phony target: all not declared");
	}

	#[test]
	fn ignores_non_matching_lines() {
		assert!(parse(b"some header line\n", b"", 0).is_empty());
	}
}
