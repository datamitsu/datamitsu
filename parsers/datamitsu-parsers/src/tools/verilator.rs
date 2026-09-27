//! verilator — Verilog and SystemVerilog linter powered by Verilator.
//! Ported from the none-ls diagnostics/verilator builtin.
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "verilator",
	description: "Verilog and SystemVerilog linter power by Verilator",
	url: "https://www.veripool.org/verilator/",
	severities: &[Level("Error", severity::ERROR), Level("Warning", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["-lint-only", "-Wno-fatal", "{file}"],
		stdin: false,
	}],
};

// Diagnostics come from stderr (from_stderr = true). Upstream Lua pattern:
//   %%(%w+).*<bufname>:(%d+):(%d+): (.*)
// e.g. "%Error: top.sv:10:5: syntax error" or "%Warning-WIDTH: top.sv:22:13: …".
// We match the leading "%<Word>" level token and the optional "-<CODE>" after it,
// then the trailing "<file>:row:col: message" tail; the path segment between is
// matched loosely (".*") since the bufname is not known here.
pub fn parse(_stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	String::from_utf8_lossy(stderr).lines().filter_map(parse_line).collect()
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	// Must begin with "%<Word>" (e.g. %Error, %Warning).
	let rest = line.strip_prefix('%')?;
	let sev_end = rest.find(|c: char| !c.is_alphanumeric())?;
	if sev_end == 0 {
		return None;
	}
	let sev_token = &rest[..sev_end];
	let code = rest[sev_end..]
		.strip_prefix('-')
		.and_then(|s| s.split_once(':'))
		.map(|(code, _)| code)
		.filter(|code| !code.is_empty() && code.chars().all(|c| c.is_ascii_alphanumeric() || c == '_'));

	// Find the trailing "<file>:row:col: message" by scanning from the right.
	// Locate the ": " separating "col:" from the message.
	let (locus, message) = split_locus(rest)?;
	let (file, row, col) = parse_row_col(locus)?;

	Some(RawDiagnostic {
		message: message.to_string(),
		row: Some(row),
		col: Some(col),
		severity: severity::of(DESCRIPTOR.severities, sev_token),
		code: code.map(str::to_string),
		file: file.and_then(crate::diagnostic::file_field),
		..RawDiagnostic::default()
	})
}

/// Splits a tail like "...:10:5: syntax error" into ("...:10:5", "syntax error").
fn split_locus(s: &str) -> Option<(&str, &str)> {
	// Find the right-most ":<digits>:<digits>: " by working back from message.
	// We look for the ": " that follows the col number.
	let bytes = s.as_bytes();
	let mut i = 0;
	let mut best: Option<usize> = None;
	while let Some(pos) = s[i..].find(": ") {
		let abs = i + pos;
		// Require the char immediately before ": " to be a digit (col number).
		if abs > 0 && bytes[abs - 1].is_ascii_digit() {
			best = Some(abs);
		}
		i = abs + 1;
	}
	let abs = best?;
	Some((&s[..abs], &s[abs + 2..]))
}

/// Parses "<Level[-CODE]>: <file>:row:col" into the file and the position.
fn parse_row_col(locus: &str) -> Option<(Option<&str>, u32, u32)> {
	let (head, col_str) = locus.rsplit_once(':')?;
	let (head, row_str) = head.rsplit_once(':')?;
	let col = col_str.parse().ok()?;
	let row = row_str.parse().ok()?;
	Some((head.split_once(": ").map(|(_, file)| file), row, col))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_error_and_warning() {
		let stderr = b"%Error: top.sv:10:5: syntax error, unexpected ';'\n\
                       %Warning-WIDTH: top.sv:22:13: Operator has a width mismatch\n";
		let diags = parse(b"", stderr, 1);
		assert_eq!(diags.len(), 2);

		assert_eq!(diags[0].message, "syntax error, unexpected ';'");
		assert_eq!(diags[0].row, Some(10));
		assert_eq!(diags[0].col, Some(5));
		assert_eq!(diags[0].severity, Some(severity::ERROR));
		assert_eq!(diags[0].code, None);

		assert_eq!(diags[1].row, Some(22));
		assert_eq!(diags[1].col, Some(13));
		// "Warning-WIDTH" is not exactly "Warning"; the %w+ token is "Warning".
		assert_eq!(diags[1].severity, Some(severity::WARNING));
		assert_eq!(diags[1].code.as_deref(), Some("WIDTH"));
		assert_eq!(diags[1].message, "Operator has a width mismatch");
	}

	#[test]
	fn reads_the_code_of_an_error_and_leaves_an_unlisted_level_unset() {
		let d = parse_line("%Error-PROCASSWIRE: top.sv:4:9: Procedural assignment to wire").unwrap();
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.code.as_deref(), Some("PROCASSWIRE"));
		let d = parse_line("%Info: top.sv:1:1: note").unwrap();
		assert_eq!(d.severity, None);
	}

	#[test]
	fn ignores_non_diagnostic_lines() {
		let stderr = b"some banner line without locus\n";
		assert!(parse(b"", stderr, 0).is_empty());
	}

	#[test]
	fn each_finding_names_its_file() {
		let stderr = b"%Error: rtl/top.sv:1:1: first\n%Warning-WIDTH: rtl/alu.sv:2:3: second\n";
		let files: Vec<_> = parse(b"", stderr, 1).into_iter().map(|d| d.file).collect();
		assert_eq!(files, [Some("rtl/top.sv".to_string()), Some("rtl/alu.sv".to_string())]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: b"",
		stderr: b"%Warning-WIDTHTRUNC: top.sv:22:13: Operator ASSIGNW expects 4 bits on the Assign RHS, but Assign RHS's VARREF 'b' generates 8 bits.\n                                   : ... note: In instance 'top'\n   22 |   assign a = b;\n      |             ^\n                     ... For warning description see https://verilator.org/warn/WIDTHTRUNC?v=5.030\n                     ... Use \"/* verilator lint_off WIDTHTRUNC */\" and lint_on around source to disable this message.\n%Error: top.sv:30:1: syntax error, unexpected endmodule\n   30 | endmodule\n      | ^~~~~~~~~\n%Error: Exiting due to 1 error(s)\n",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"%Warning-UNUSEDSIGNAL: top.sv:5:15: Signal is not used: 'unused'\n                                    : ... note: In instance 'top'\n    5 |   logic [3:0] unused;\n      |               ^~~~~~\n                        ... For warning description see https://verilator.org/warn/UNUSEDSIGNAL?v=5.030\n",
		exit: 0,
	},
];
