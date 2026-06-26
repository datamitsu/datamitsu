//! zsh — evaluates (but does not execute) zsh scripts via `zsh -n`. Ported from the none-ls diagnostics/zsh builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "zsh",
    description: "Uses zsh's own -n option to evaluate, but not execute, zsh scripts. Effectively, this acts somewhat like a linter, although it only really checks for serious errors - and will likely only show the first error.",
    url: "https://www.zsh.org/",
    operations: &[Operation {
        mode: "lint",
        args: &["-n", "{file}"],
        stdin: false,
    }],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Pattern: (.+):(%d+): (.+) -> filename, row, message
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Find the ":<digits>: " separator after the filename.
	// Filename may contain colons, so locate the first ":<digits>:" group.
	let bytes = line.as_bytes();
	let mut search_from = 0;
	loop {
		let rel = line[search_from..].find(':')?;
		let colon = search_from + rel;
		// Parse digits after this colon.
		let mut end = colon + 1;
		while end < bytes.len() && bytes[end].is_ascii_digit() {
			end += 1;
		}
		let has_digits = end > colon + 1;
		if has_digits && end < bytes.len() && bytes[end] == b':' {
			let row: u32 = line[colon + 1..end].parse().ok()?;
			// Expect ": " then message.
			let rest = &line[end + 1..];
			let message = rest.strip_prefix(' ').unwrap_or(rest);
			if message.is_empty() {
				return None;
			}
			return Some(RawDiagnostic {
				message: message.to_string(),
				row: Some(row),
				..RawDiagnostic::default()
			});
		}
		search_from = colon + 1;
		if search_from >= line.len() {
			return None;
		}
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_line() {
		let stderr = b"script.zsh:5: parse error near `done'\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].message, "parse error near `done'");
		assert_eq!(diags[0].row, Some(5));
	}

	#[test]
	fn ignores_non_matching_lines() {
		let stderr = b"some unrelated output\nfile.zsh:12: missing end of string\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(12));
		assert_eq!(diags[0].message, "missing end of string");
	}
}
