//! harper-cli — a grammar and style checker. `--format compact` output line:
//!
//! ```text
//! <file>:<row>:<col>: <code>: <message>
//! ```
//!
//! e.g. `text.md:1:24: Miscellaneous::AnA: Incorrect indefinite article.`. The
//! rule code itself contains `::` (namespace::name), so the location is split off
//! at the first `": "` (which follows the column), and the code/message at the
//! next. No severity token → the core supplies its fallback. Informational lines
//! ("Note: …", "Error: …") have no numeric col and are skipped.

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "harper_cli",
	description: "A grammar and style checker.",
	url: "https://writewithharper.com",
	operations: &[Operation {
		mode: "lint",
		args: &["lint", "--format", "compact", "{files}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let bytes = if stdout.is_empty() { stderr } else { stdout };
	String::from_utf8_lossy(bytes).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// The first ": " follows the column; everything before it is "file:row:col".
	let sep = line.find(": ")?;
	let mut it = line[..sep].rsplit(':');
	let col: u32 = it.next()?.trim().parse().ok()?;
	let row: u32 = it.next()?.trim().parse().ok()?;

	// "code: message" — code may contain "::", so split at the next ": ".
	let rest = &line[sep + 2..];
	let (code, message) = match rest.find(": ") {
		Some(i) => (rest[..i].trim().to_string(), rest[i + 2..].trim().to_string()),
		None => (String::new(), rest.trim().to_string()),
	};
	if message.is_empty() {
		return None;
	}
	Some(RawDiagnostic {
		message,
		row: Some(row),
		col: Some(col),
		code: if code.is_empty() { None } else { Some(code) },
		..RawDiagnostic::default()
	})
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_code_with_double_colon() {
		let d = parse_line("text.md:1:24: Miscellaneous::AnA: Incorrect indefinite article.").unwrap();
		assert_eq!((d.row, d.col), (Some(1), Some(24)));
		assert_eq!(d.code.as_deref(), Some("Miscellaneous::AnA"));
		assert_eq!(d.message, "Incorrect indefinite article.");
	}

	#[test]
	fn skips_informational_lines() {
		assert!(parse_line("Note: There is no user dictionary at /home/x/dictionary.txt").is_none());
		assert!(parse_line("Error: Lints were found").is_none());
	}

	#[test]
	fn collects_multiple() {
		let out = parse(
            b"a.md:1:24: Miscellaneous::AnA: Incorrect indefinite article.\na.md:1:20: Agreement::PronounVerbAgreement: Verb must agree.\n",
            b"",
            1,
        );
		assert_eq!(out.len(), 2);
		assert_eq!(out[1].code.as_deref(), Some("Agreement::PronounVerbAgreement"));
	}
}
