//! bean_check — Beancount: text-based double-entry accounting tool.
//! Ported from the none-ls diagnostics/bean_check builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "bean_check",
	description: "Beancount: text-based double-entry accounting tool",
	url: "https://github.com/beancount/beancount",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-"],
		stdin: true,
	}],
};

pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// bean-check reports via stderr (from_stderr = true).
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

// Pattern: (.+):(%d+):%s*(.+) -> groups filename, row, message.
// Lua's .+ for filename is greedy, but (%d+) forces the row to be the LAST
// numeric segment between colons, with the message following the next colon.
fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Find a "<filename>:<digits>:" prefix where <digits> is the last such
	// colon-delimited number before the message. Greedy filename => scan from
	// the right for the rightmost ":<digits>:" boundary.
	let bytes = line.as_bytes();
	// Locate candidate "<digits>:" segments; greedy .+ takes as much as possible,
	// so pick the rightmost colon-delimited all-digit group whose tail is ":msg".
	let mut search_from = line.len();
	while let Some(rel) = line[..search_from].rfind(':') {
		// rel is the colon before the message: <...>:<row>:<message>
		let message = &line[rel + 1..];
		let head = &line[..rel]; // <filename>:<row>
		if let Some(row_colon) = head.rfind(':') {
			let row_str = &head[row_colon + 1..];
			if !row_str.is_empty() && row_str.bytes().all(|b| b.is_ascii_digit()) {
				let filename = &head[..row_colon];
				if !filename.is_empty() {
					let row: u32 = row_str.parse().ok()?;
					let message = message.trim_start().to_string(); // %s* before message
					if message.is_empty() {
						return None;
					}
					return Some(RawDiagnostic {
						message,
						row: Some(row),
						// An error in an included ledger names that ledger.
						file: crate::diagnostic::file_field(filename),
						..RawDiagnostic::default()
					});
				}
			}
		}
		search_from = rel;
	}
	let _ = bytes;
	None
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_basic() {
		let stderr = b"/path/to/ledger.beancount:42:  Transaction does not balance: (1.00 USD)\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(42));
		assert_eq!(diags[0].message, "Transaction does not balance: (1.00 USD)");
	}

	#[test]
	fn message_with_colon() {
		let stderr = b"main.beancount:10: Invalid account: Assets:Cash\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 1);
		assert_eq!(diags[0].row, Some(10));
		assert_eq!(diags[0].message, "Invalid account: Assets:Cash");
	}

	#[test]
	fn ignores_non_matching() {
		let diags = parse(b"", b"some unrelated banner line\n", 1);
		assert!(diags.is_empty());
	}

	#[test]
	fn each_finding_names_its_file() {
		let stderr = b"main.beancount:10: Invalid account: Assets:Cash\nincludes/2024.beancount:3:  Duplicate entry\n";
		let files: Vec<_> = parse(b"", stderr, 1).into_iter().map(|d| d.file).collect();
		assert_eq!(
			files,
			[
				Some("main.beancount".to_string()),
				Some("includes/2024.beancount".to_string())
			]
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"",
	stderr: b"/work/ledger.beancount:42:  Transaction does not balance: (1.00 USD)\n\n   2024-01-05 * \"Coffee\"\n     Expenses:Food   1.00 USD\n\n/work/ledger.beancount:57:  Invalid reference to unknown account 'Assets:Cash'\n",
	exit: 1,
}];
