//! phpcs — PHP_CodeSniffer. Ported from the none-ls diagnostics/phpcs builtin.
//!
//! phpcs `--report=json` emits `{"files": {"<path>": {"messages": [...]}}}` where
//! each message is `{"message","source","severity","type","line","column",...}`.
//! none-ls navigates to `output.files[bufname].messages` and runs `from_json` with
//! `severity = "type"` (ERROR/WARNING tokens) and `code = "source"`; line/column
//! use the defaults. We iterate every file's messages (the WASM core has no single
//! buffer name to key on) and reuse the field mapping via `from_obj`.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "phpcs",
	description: "PHP_CodeSniffer is a script that tokenizes PHP, JavaScript and CSS files to detect violations of a defined coding standard.",
	url: "https://github.com/squizlabs/PHP_CodeSniffer",
	severities: &[Level("ERROR", severity::ERROR), Level("WARNING", severity::WARNING)],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &[
			"--report=json",
			"-q",
			"-s",
			"--runtime-set",
			"ignore_warnings_on_exit",
			"1",
			"--runtime-set",
			"ignore_errors_on_exit",
			"1",
			"--stdin-path={file}",
			"--basepath=",
		],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	let value: JsonValue = match text.parse() {
		Ok(v) => v,
		Err(_) => return Vec::new(),
	};

	// none-ls overrides: severity comes from the "type" token, code from "source".
	let attrs = Attrs {
		severity: "type",
		code: "source",
		..Attrs::defaults()
	};

	let mut out = Vec::new();
	if let JsonValue::Object(root) = &value {
		if let Some(JsonValue::Object(files)) = root.get("files") {
			for (path, file) in files {
				// phpcs keys what it read from stdin without --stdin-path as "STDIN".
				let path = crate::diagnostic::file_field(path).filter(|p| p != "STDIN");
				if let JsonValue::Object(fmap) = file {
					if let Some(JsonValue::Array(messages)) = fmap.get("messages") {
						for msg in messages {
							if let Some(mut d) = json_diag::from_obj(msg, &attrs, severity_of) {
								d.file.clone_from(&path);
								out.push(d);
							}
						}
					}
				}
			}
		}
	}
	out
}

/// phpcs emits the level as the uppercase `type` token.
fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_messages_across_files() {
		let out = parse(REPORT, b"", 0);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Missing file doc comment");
		assert_eq!(out[0].row, Some(1));
		assert_eq!(out[0].col, Some(1));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].code.as_deref(), Some("PEAR.Commenting.FileComment.Missing"));
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].row, Some(12));
	}

	#[test]
	fn an_unknown_type_sets_none() {
		let json = br#"{"files":{"a.php":{"messages":[{"message":"x","type":"NOTICE","line":1,"column":1}]}}}"#;
		assert_eq!(parse(json, b"", 0)[0].severity, None);
	}

	#[test]
	fn empty_files_yield_nothing() {
		let json = br#"{"totals":{"errors":0,"warnings":0},"files":{}}"#;
		assert!(parse(json, b"", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"phpcs status message", b"", 0).is_empty());
	}

	#[test]
	fn each_finding_names_the_file_it_is_keyed_under() {
		let json = br#"{"files":{
            "src/a.php":{"messages":[{"message":"first","type":"ERROR","line":1,"column":1}]},
            "src/b.php":{"messages":[{"message":"second","type":"WARNING","line":2,"column":1}]},
            "STDIN":{"messages":[{"message":"piped","type":"ERROR","line":3,"column":1}]}}}"#;
		let mut got: Vec<_> = parse(json, b"", 0).into_iter().map(|d| (d.message, d.file)).collect();
		got.sort();
		assert_eq!(
			got,
			[
				("first".to_string(), Some("src/a.php".to_string())),
				("piped".to_string(), None),
				("second".to_string(), Some("src/b.php".to_string())),
			]
		);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: REPORT,
		stderr: b"",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: REPORT,
		stderr: b"",
		exit: 2,
	},
];

#[cfg(test)]
const REPORT: &[u8] = br#"{
    "totals": {"errors": 1, "warnings": 1, "fixable": 1},
    "files": {
        "/src/foo.php": {
            "errors": 1,
            "warnings": 1,
            "messages": [
                {"message":"Missing file doc comment","source":"PEAR.Commenting.FileComment.Missing","severity":5,"fixable":false,"type":"ERROR","line":1,"column":1},
                {"message":"Line indented incorrectly; expected 4 spaces, found 2","source":"Generic.WhiteSpace.ScopeIndent.Incorrect","severity":5,"fixable":true,"type":"WARNING","line":12,"column":3}
            ]
        }
    }
}"#;
