//! rpmspec — Command line tool to parse RPM spec files. Ported from the none-ls diagnostics/rpmspec builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "rpmspec",
	description: "Command line tool to parse RPM spec files.",
	url: "https://rpm.org/",
	operations: &[Operation {
		mode: "lint",
		args: &["-P", "{file}"],
		stdin: false,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// from_stderr = true: diagnostics are read from stderr.
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		"information" => Some(severity::INFO),
		"hint" => Some(severity::HINT),
		_ => None,
	}
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Pattern 1 (error): "(%w+): (.*): line (%d+): (.*)"
	//   groups = severity, filename, row, message
	if let Some(d) = parse_error(line) {
		return Some(d);
	}
	// Pattern 2 (warning): "(%w+): (.*) in line (%d+):"
	//   groups = severity, message, row
	parse_warning(line)
}

// "(%w+): (.*): line (%d+): (.*)"
fn parse_error(line: &str) -> Option<RawDiagnostic> {
	let (sev_word, rest) = split_word_colon(line)?;
	// rest = "(.*): line (%d+): (.*)" — find " line " preceded by ": ".
	let marker = ": line ";
	let idx = rest.rfind(marker)?;
	let filename = &rest[..idx];
	let after = &rest[idx + marker.len()..];
	// after = "(%d+): (.*)"
	let (row_str, message) = after.split_once(": ")?;
	let row: u32 = row_str.parse().ok()?;
	if message.is_empty() {
		return None;
	}
	let _ = filename;
	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		severity: severity_of(sev_word),
		..RawDiagnostic::default()
	})
}

// "(%w+): (.*) in line (%d+):"
fn parse_warning(line: &str) -> Option<RawDiagnostic> {
	let (sev_word, rest) = split_word_colon(line)?;
	// rest = "(.*) in line (%d+):"
	let line_trimmed = rest.strip_suffix(':')?;
	let marker = " in line ";
	let idx = line_trimmed.rfind(marker)?;
	let message = &line_trimmed[..idx];
	let row_str = &line_trimmed[idx + marker.len()..];
	let row: u32 = row_str.parse().ok()?;
	if message.is_empty() {
		return None;
	}
	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		severity: severity_of(sev_word),
		..RawDiagnostic::default()
	})
}

// Match leading "(%w+): " — a word (alnum + underscore) followed by ": ".
fn split_word_colon(line: &str) -> Option<(&str, &str)> {
	let (word, rest) = line.split_once(": ")?;
	if word.is_empty() || !word.chars().all(|c| c.is_alphanumeric() || c == '_') {
		return None;
	}
	Some((word, rest))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error() {
		let stderr = b"error: foo.spec: line 12: Unknown tag: Frobnicate\n";
		let d = parse(b"", stderr, 1);
		assert_eq!(d.len(), 1);
		assert_eq!(d[0].message, "Unknown tag: Frobnicate");
		assert_eq!(d[0].row, Some(12));
		assert_eq!(d[0].severity, Some(severity::ERROR));
	}

	#[test]
	fn parses_warning() {
		let stderr = b"warning: bogus date in line 5:\n";
		let d = parse(b"", stderr, 1);
		assert_eq!(d.len(), 1);
		assert_eq!(d[0].message, "bogus date");
		assert_eq!(d[0].row, Some(5));
		assert_eq!(d[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn ignores_unmatched() {
		let stderr = b"some unrelated banner output\n";
		assert_eq!(parse(b"", stderr, 0).len(), 0);
	}
}
