//! mypy — optional static type checker for Python. Ported from the none-ls
//! diagnostics/mypy builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
    name: "mypy",
    description: "Mypy is an optional static type checker for Python that aims to combine the benefits of dynamic (or \"duck\") typing and static typing.",
    url: "https://github.com/python/mypy",
    operations: &[Operation {
        mode: "lint",
        args: &[
            "--hide-error-codes",
            "--hide-error-context",
            "--no-color-output",
            "--show-absolute-path",
            "--show-column-numbers",
            "--show-error-codes",
            "--no-error-summary",
            "--no-pretty",
            "{file}",
        ],
        stdin: false,
    }],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stdout).lines().filter_map(parse_line).collect()
}

// Three patterns, tried most-specific first:
//   1. filename:row:col: severity: message  [code]
//   2. filename:row:col: severity: message
//   3. filename:row: severity: message
// filename uses Lua [^:]+ (stops at first colon).
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// filename: up to first ':'
	let (_filename, rest) = line.split_once(':')?;
	// row: digits up to ':'
	let (row_s, rest) = rest.split_once(':')?;
	let row: u32 = row_s.trim().parse().ok()?;

	// Try col next. Pattern 1/2 have "col: severity: message"; pattern 3 has
	// "severity: message" (no col).
	let (col, rest) = match rest.split_once(':') {
		Some((maybe_col, after)) => match maybe_col.trim().parse::<u32>() {
			Ok(c) => (Some(c), after),
			// Not a number -> no column form: `maybe_col` is the severity start.
			Err(_) => (None, rest),
		},
		None => (None, rest),
	};

	// rest now: " severity: message[  [code]]"
	let (sev_s, message) = rest.split_once(':')?;
	let sev_s = sev_s.trim();
	let message = message.trim_start();
	if message.is_empty() {
		return None;
	}

	// Optional trailing error code: message ends with "  [code]".
	let (message, code) = split_code(message);

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col,
		severity: severity_of(sev_s),
		code,
		..RawDiagnostic::default()
	})
}

// Lua tail: (.*)  %[([%a-]+)%] — message, two spaces, "[code]" where code is
// letters and hyphens. Returns (message_without_code, optional_code).
fn split_code(message: &str) -> (&str, Option<String>) {
	if let Some(stripped) = message.strip_suffix(']') {
		if let Some(open) = stripped.rfind("  [") {
			let code = &stripped[open + 3..];
			if !code.is_empty() && code.chars().all(|c| c.is_ascii_alphabetic() || c == '-') {
				return (&message[..open], Some(code.to_string()));
			}
		}
	}
	(message, None)
}

fn severity_of(level: &str) -> Option<u8> {
	match level {
		"error" => Some(severity::ERROR),
		"warning" => Some(severity::WARNING),
		"note" => Some(severity::INFO),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_with_code() {
		let stdout =
			b"/src/app.py:10:5: error: Incompatible return value type (got \"int\", expected \"str\")  [return-value]\n";
		let diags = parse(stdout, &[], 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(10));
		assert_eq!(diags[0].col, Some(5));
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[0].code.as_deref(), Some("return-value"));
		assert_eq!(
			diags[0].message,
			"Incompatible return value type (got \"int\", expected \"str\")"
		);
	}

	#[test]
	fn parses_note_without_code() {
		let stdout = b"/src/app.py:12:9: note: Revealed type is \"builtins.int\"\n";
		let diags = parse(stdout, &[], 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].col, Some(9));
		assert_eq!(diags[0].severity, Some(severity::INFO));
		assert_eq!(diags[0].code, None);
		assert_eq!(diags[0].message, "Revealed type is \"builtins.int\"");
	}

	#[test]
	fn parses_warning_without_column() {
		let stdout = b"/src/app.py:3: warning: unused 'type: ignore' comment\n";
		let diags = parse(stdout, &[], 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(3));
		assert_eq!(diags[0].col, None);
		assert_eq!(diags[0].severity, Some(severity::WARNING));
		assert_eq!(diags[0].message, "unused 'type: ignore' comment");
	}
}
