//! hadolint — a smarter Dockerfile linter.
//!
//! Ported from the none-ls `diagnostics/hadolint` builtin (the JSON class). It
//! runs `hadolint --no-fail --format=json -` (stdin) and emits a JSON array of
//! `{file,line,column,level,code,message}`. none-ls overrides the code attribute
//! to `code` (not the default `ruleId`) and maps `info → information`,
//! `style → hint`.

use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "hadolint",
	description: "A smarter Dockerfile linter that helps you build best-practice Docker images.",
	url: "https://github.com/hadolint/hadolint",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("info", severity::INFO),
		Level("style", severity::HINT),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--no-fail", "--format=json", "-"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let attrs = Attrs {
		// hadolint names the rule id "code", not the default "ruleId".
		code: "code",
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

	pub(super) const SAMPLE: &[u8] = br#"[
      {"file":"-","line":3,"column":1,"level":"warning","code":"DL3008","message":"Pin versions in apt get install"},
      {"file":"-","line":7,"column":1,"level":"info","code":"DL3059","message":"Multiple consecutive RUN instructions"},
      {"file":"-","line":9,"column":1,"level":"style","code":"DL3015","message":"Avoid additional packages"}
    ]"#;

	#[test]
	fn parses_array_with_code_and_level_mapping() {
		let out = parse(SAMPLE, b"", 0);
		assert_eq!(out.len(), 3);

		assert_eq!(out[0].message, "Pin versions in apt get install");
		assert_eq!((out[0].row, out[0].col), (Some(3), Some(1)));
		assert_eq!(out[0].code.as_deref(), Some("DL3008"));
		assert_eq!(out[0].severity, Some(severity::WARNING));

		// info → INFO(3), style → HINT(4) (none-ls overrides).
		assert_eq!(out[1].severity, Some(severity::INFO));
		assert_eq!(out[2].severity, Some(severity::HINT));
		assert_eq!(out[2].code.as_deref(), Some("DL3015"));
	}

	#[test]
	fn maps_error_and_leaves_an_unknown_level_unset() {
		let json = br#"[{"line":2,"column":1,"level":"error","code":"DL3000","message":"Use absolute WORKDIR"},
            {"line":3,"column":1,"level":"ignore","code":"DL3001","message":"m"}]"#;
		let out = parse(json, b"", 1);
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[1].severity, None);
	}

	#[test]
	fn clean_run_empty_array_yields_nothing() {
		assert!(parse(b"[]", b"", 0).is_empty());
	}
	#[test]
	fn reports_the_path_but_not_the_stdin_placeholder() {
		// The bundled recipe pipes stdin, where hadolint prints "-" — a placeholder
		// the core must replace with the file it linted.
		assert!(parse(SAMPLE, b"", 1).iter().all(|d| d.file.is_none()));

		let with_path =
			br#"[{"file":"docker/Dockerfile","line":1,"column":1,"level":"warning","code":"DL3006","message":"Always tag"}]"#;
		assert_eq!(parse(with_path, b"", 1)[0].file.as_deref(), Some("docker/Dockerfile"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: tests::SAMPLE,
		stderr: b"",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: br#"[{"code":"DL3006","column":1,"file":"Dockerfile","level":"warning","line":1,"message":"Always tag the version of an image explicitly"},{"code":"DL3000","column":1,"file":"Dockerfile","level":"error","line":2,"message":"Use absolute WORKDIR"}]"#,
		stderr: b"",
		exit: 1,
	},
];
