//! vale — syntax-aware prose linter. Ported from the none-ls diagnostics/vale builtin.
//!
//! Vale's JSON output (`--output JSON`) is not a flat array: it is an object
//! keyed by filename (or `stdin.<ext>` when reading stdin), each value an array
//! of diagnostic objects with PascalCase fields — `Line`, `Span` ([start, end]),
//! `Check`, `Message`, `Severity`, `Link`. This shape (filename map + the `Span`
//! pair) does not fit `json_diag::from_json`, so the mapping is hand-rolled over
//! tinyjson. row=Line; `Span` is the 1-based first and last column of the match,
//! so col=Span[1] and end_col=Span[2]+1; code=Check, message=Message, url=Link
//! when non-empty, severity from {error, warning, suggestion}.
use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "vale",
	description: "Syntax-aware linter for prose built with speed and extensibility in mind.",
	url: "https://vale.sh/",
	severities: &[
		Level("error", severity::ERROR),
		Level("warning", severity::WARNING),
		Level("suggestion", severity::HINT),
	],
	column_unit: "utf-32",
	category: "",
	kind: "tool",
	// to_stdin=true; vale reads the buffer on stdin. `--ext` is per-file in the
	// builtin (it derives the extension from the bufname); a representative
	// markdown extension stands in for the static recipe.
	operations: &[Operation {
		mode: "lint",
		args: &["--no-exit", "--output", "JSON", "--ext", ".md"],
		stdin: true,
	}],
};

fn severity_of(level: &str) -> Option<u8> {
	severity::of(DESCRIPTOR.severities, level)
}

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// Lenient: vale runs over a whole repository, where anything else writing to
	// stdout would otherwise cost every diagnostic in the run.
	crate::tools::json_diag::extract_lenient(stdout, from_report)
}

/// vale names the buffer it read from stdin `stdin.<ext>`; a file of that name
/// at the root of a batch is indistinguishable and loses its name too.
fn is_stdin_buffer(name: &str) -> bool {
	name.starts_with("stdin.") && !name.contains(['/', '\\'])
}

fn from_report(value: &JsonValue) -> Vec<RawDiagnostic> {
	let mut out = Vec::new();
	// Top level is an object keyed by filename; iterate every file's array.
	if let JsonValue::Object(files) = value {
		for (name, items) in files {
			if let JsonValue::Array(arr) = items {
				for it in arr {
					if let JsonValue::Object(obj) = it {
						if let Some(mut d) = from_obj(obj) {
							// The key is the only place the path appears, and one
							// vale run covers many files.
							d.file = crate::diagnostic::file_field(name).filter(|f| !is_stdin_buffer(f));
							out.push(d);
						}
					}
				}
			}
		}
	}
	out
}

fn from_obj(obj: &HashMap<String, JsonValue>) -> Option<RawDiagnostic> {
	let message = match obj.get("Message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	let (col, end_col) = match obj.get("Span") {
		Some(JsonValue::Array(span)) => {
			let start = span.first().and_then(as_u32);
			// The second entry is the match's last column: +1 makes it exclusive.
			let end = span.get(1).and_then(as_u32).and_then(|e| e.checked_add(1));
			(start, end)
		}
		_ => (None, None),
	};
	let severity = match obj.get("Severity") {
		Some(JsonValue::String(s)) => severity_of(s),
		_ => None,
	};
	Some(RawDiagnostic {
		message,
		row: obj.get("Line").and_then(as_u32),
		col,
		end_col,
		code: string_field(obj, "Check"),
		url: string_field(obj, "Link").filter(|s| !s.is_empty()),
		severity,
		..RawDiagnostic::default()
	})
}

fn string_field(obj: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match obj.get(key) {
		Some(JsonValue::String(s)) => Some(s.clone()),
		_ => None,
	}
}

fn as_u32(v: &JsonValue) -> Option<u32> {
	match v {
		JsonValue::Number(n) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_vale_json_keyed_by_filename() {
		let json = br#"{
            "stdin.md": [
                {
                    "Check": "Vale.Spelling",
                    "Line": 4,
                    "Message": "Did you really mean 'mispeled'?",
                    "Severity": "error",
                    "Span": [10, 18]
                },
                {
                    "Check": "write-good.Weasel",
                    "Line": 7,
                    "Message": "'very' is a weasel word!",
                    "Severity": "suggestion",
                    "Span": [1, 4]
                }
            ]
        }"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 2);

		assert_eq!(out[0].message, "Did you really mean 'mispeled'?");
		assert_eq!(out[0].row, Some(4));
		assert_eq!(out[0].col, Some(10));
		assert_eq!(out[0].end_col, Some(19)); // Span[2] + 1
		assert_eq!(out[0].code.as_deref(), Some("Vale.Spelling"));
		assert_eq!(out[0].severity, Some(severity::ERROR));

		assert_eq!(out[1].severity, Some(severity::HINT));
		assert_eq!(out[1].col, Some(1));
		assert_eq!(out[1].end_col, Some(5));
	}

	#[test]
	fn reads_the_link_and_every_level() {
		let s = &SAMPLES[1];
		let out = parse(s.stdout, s.stderr, s.exit);
		assert_eq!(out.len(), 2);
		assert_eq!(
			out[0].url.as_deref(),
			Some("https://developers.google.com/style/contractions")
		);
		assert_eq!(out[1].url, None);
		let levels: Vec<_> = out.iter().map(|d| d.severity).collect();
		assert_eq!(levels, [Some(severity::HINT), Some(severity::WARNING)]);

		let unlisted = br#"{"a.md":[{"Check":"c","Line":1,"Message":"m","Severity":"notice","Span":[1,2]}]}"#;
		assert_eq!(parse(unlisted, b"", 0)[0].severity, None);
	}

	#[test]
	fn span_is_the_inclusive_last_column() {
		// Measured with vale 3.18: "very very" starting at column 5 spans [5, 13].
		let json = br#"{"a.md":[{"Check":"Vale.Repetition","Line":1,"Link":"","Match":"very very","Message":"'very' is repeated!","Severity":"error","Span":[5,13]}]}"#;
		let d = &parse(json, b"", 1)[0];
		assert_eq!((d.col, d.end_col), (Some(5), Some(14)));
		assert_eq!(d.url, None);
	}

	#[test]
	fn empty_object_yields_nothing() {
		assert!(parse(b"{}", b"", 0).is_empty());
	}

	#[test]
	fn invalid_json_yields_nothing() {
		assert!(parse(b"not json", b"", 1).is_empty());
	}
	#[test]
	fn reports_the_keyed_filename_and_drops_stdin() {
		let json = br#"{"docs/a.md":[{"Check":"c","Line":2,"Message":"m","Severity":"error","Span":[1,2]}]}"#;
		let out = parse(json, b"", 1);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].file.as_deref(), Some("docs/a.md"));

		// vale names the stdin buffer "stdin.<ext>" — a placeholder, not a file.
		let stdin = br#"{"stdin.md":[{"Check":"c","Line":2,"Message":"m","Severity":"error","Span":[1,2]}]}"#;
		assert_eq!(parse(stdin, b"", 1)[0].file, None);
	}

	#[test]
	fn skips_noise_printed_before_the_json() {
		let noisy = br#"loading config...
{"docs/a.md":[{"Check":"c","Line":2,"Message":"m","Severity":"error","Span":[1,2]}]}"#;
		assert_eq!(parse(noisy, b"", 1).len(), 1);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br#"{
  "docs/a.md": [
    {
      "Action": {
        "Name": "edit",
        "Params": [
          "truncate",
          " "
        ]
      },
      "Span": [
        6,
        10
      ],
      "Check": "Vale.Repetition",
      "Description": "",
      "Link": "",
      "Message": "'is' is repeated!",
      "Severity": "error",
      "Match": "is is",
      "Line": 1
    }
  ]
}
"#,
		stderr: b"",
		exit: 1,
	},
	crate::contract::Sample {
		stdout: br#"{
  "docs/b.md": [
    {
      "Action": {
        "Name": "replace",
        "Params": [
          "don't"
        ]
      },
      "Span": [
        1,
        6
      ],
      "Check": "Google.Contractions",
      "Description": "",
      "Link": "https://developers.google.com/style/contractions",
      "Message": "Use 'don't' instead of 'do not'.",
      "Severity": "suggestion",
      "Match": "do not",
      "Line": 3
    },
    {
      "Action": {
        "Name": "",
        "Params": null
      },
      "Span": [
        12,
        19
      ],
      "Check": "Google.Passive",
      "Description": "",
      "Link": "",
      "Message": "In general, use active voice instead of passive voice ('is used').",
      "Severity": "warning",
      "Match": "is used",
      "Line": 5
    }
  ]
}
"#,
		stderr: b"",
		exit: 0,
	},
];
