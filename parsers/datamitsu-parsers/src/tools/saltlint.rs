//! saltlint — checks for best practices in SaltStack. Ported from the none-ls
//! diagnostics/saltlint builtin.
//!
//! `--json` prints an array of `{id, message, filename, linenumber, line,
//! severity}`; `severity` is the rule's level (`VERY_HIGH` … `INFO`). The line
//! is 1-based and no column is printed.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "saltlint",
	description: "A command-line utility that checks for best practices in SaltStack.",
	url: "https://github.com/warpnet/salt-lint",
	severities: &[
		Level("VERY_HIGH", severity::ERROR),
		Level("HIGH", severity::ERROR),
		Level("MEDIUM", severity::WARNING),
		Level("LOW", severity::WARNING),
		Level("VERY_LOW", severity::INFO),
		Level("INFO", severity::INFO),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--nocolor", "--json", "{file}"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let attrs = Attrs {
		row: "linenumber",
		code: "id",
		message: "message",
		severity: "severity",
		file: "filename",
		..Attrs::defaults()
	};
	json_diag::from_json(stdout, &attrs, severity_of)
}

fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_saltlint_json() {
		let out = parse(SAMPLES[0].stdout, b"", 2);
		assert_eq!(out.len(), 2);
		assert_eq!(
			out[0].message,
			"File modes should always be encapsulated in quotation marks"
		);
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, None);
		assert_eq!(out[0].code.as_deref(), Some("207"));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].code.as_deref(), Some("206"));
	}

	#[test]
	fn reads_every_printed_level() {
		let out = parse(SAMPLES[1].stdout, b"", 2);
		let levels: Vec<_> = out.iter().map(|d| d.severity).collect();
		assert_eq!(
			levels,
			[Some(severity::INFO), Some(severity::INFO), Some(severity::WARNING)]
		);
	}

	#[test]
	fn unknown_severity_is_none() {
		let json = br#"[{"id":"1","message":"x","linenumber":1,"severity":"NOTICE"}]"#;
		let out = parse(json, b"", 2);
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn each_finding_names_its_file() {
		let json = br#"[
            {"id":"201","message":"first","filename":"a.sls","linenumber":1,"severity":"INFO"},
            {"id":"207","message":"second","filename":"states/b.sls","linenumber":2,"severity":"HIGH"}]"#;
		let out = parse(json, b"", 2);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(got, [("first", Some("a.sls")), ("second", Some("states/b.sls"))]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"[{"id": "207", "message": "File modes should always be encapsulated in quotation marks", "filename": "foo.sls", "linenumber": 3, "line": "    - mode: 0644", "severity": "HIGH"}, {"id": "206", "message": "Jinja variables should have spaces before and after: {{ var_name }}", "filename": "foo.sls", "linenumber": 7, "line": "{{foo}}", "severity": "LOW"}]"#,
		stderr: b"",
		exit: 2,
	},
	crate::contract::Sample {
		stdout: br#"[{"id": "201", "message": "Trailing whitespace", "filename": "foo.sls", "linenumber": 2, "line": "pkg.installed: ", "severity": "INFO"}, {"id": "204", "message": "Lines should be no longer than 160 chars", "filename": "foo.sls", "linenumber": 9, "line": "x", "severity": "VERY_LOW"}, {"id": "218", "message": "Use 'source_hash' with 'source' in file.managed", "filename": "foo.sls", "linenumber": 12, "line": "file.managed:", "severity": "MEDIUM"}]"#,
		stderr: b"",
		exit: 2,
	},
];
