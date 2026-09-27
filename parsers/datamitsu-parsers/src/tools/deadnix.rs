//! deadnix — Scan Nix files for dead code. Ported from the none-ls diagnostics/deadnix builtin.
//!
//! deadnix's `--output-format=json` prints one object per file, each on its own
//! line: `{"file": "...", "results": [{"line":3,"column":5,"endColumn":12,"message":"..."}]}`.
//! none-ls navigates to `output.results` and runs `from_json` over those span
//! objects with default attributes. deadnix prints no level and no rule id, so
//! severity and code stay None.
use super::json_diag::{self, Attrs};
use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;

use tinyjson::JsonValue;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "deadnix",
	description: "Scan Nix files for dead code.",
	url: "https://github.com/astro/deadnix",
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["--output-format=json", "{file}"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stdout);
	// none-ls's `from_json` defaults; deadnix span objects use line/column/endColumn.
	let attrs = Attrs::defaults();

	let mut out = Vec::new();
	match text.parse::<JsonValue>() {
		Ok(value) => collect(&value, &attrs, &mut out),
		// Several files print one object per line, which is not one document.
		Err(_) => {
			for line in text.lines() {
				if let Ok(value) = line.trim().parse::<JsonValue>() {
					collect(&value, &attrs, &mut out);
				}
			}
		}
	}
	out
}

/// deadnix emits one top-level object per file with a `results` array, but may
/// also stream an array of such objects. Walk either shape to the spans.
fn collect(value: &JsonValue, attrs: &Attrs, out: &mut Vec<RawDiagnostic>) {
	match value {
		JsonValue::Array(items) => {
			for it in items {
				collect(it, attrs, out);
			}
		}
		JsonValue::Object(map) => {
			let file = match map.get("file") {
				Some(JsonValue::String(s)) => crate::diagnostic::file_field(s),
				_ => None,
			};
			if let Some(JsonValue::Array(results)) = map.get("results") {
				for span in results {
					if let Some(mut d) = json_diag::from_obj(span, attrs, |_| None) {
						d.file.clone_from(&file);
						out.push(d);
					}
				}
			}
		}
		_ => {}
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const SPANS: &[u8] = br#"{"file":"flake.nix","results":[
            {"line":3,"column":5,"endColumn":8,"message":"Unused declaration: foo"},
            {"line":7,"column":1,"endColumn":4,"message":"Unused lambda pattern: bar"}
        ]}"#;

	#[test]
	fn parses_dead_code_spans() {
		let out = parse(SPANS, b"", 0);
		assert_eq!(out.len(), 2);
		assert_eq!(out[0].message, "Unused declaration: foo");
		assert_eq!(out[0].row, Some(3));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_col, Some(8));
		assert_eq!(out[0].code, None);
		assert_eq!(out[1].message, "Unused lambda pattern: bar");
	}

	#[test]
	fn handles_array_of_file_objects() {
		let json = br#"[{"file":"a.nix","results":[{"line":1,"column":2,"message":"Unused declaration: x"}]}]"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
	}

	#[test]
	fn never_sets_a_severity() {
		// deadnix prints no level; a `level` key it never writes is not read either.
		let json =
			br#"{"file":"a.nix","results":[{"line":1,"column":2,"message":"Unused declaration: x","level":"error"}]}"#;
		let out = parse(SPANS, b"", 1).into_iter().chain(parse(json, b"", 1));
		assert!(out.map(|d| d.severity).all(|s| s.is_none()));
	}

	#[test]
	fn empty_results_yield_nothing() {
		let out = parse(br#"{"file":"clean.nix","results":[]}"#, b"", 0);
		assert!(out.is_empty());
	}

	#[test]
	fn every_file_line_is_read_and_names_its_findings() {
		let stdout = b"{\"file\":\"a.nix\",\"results\":[{\"line\":1,\"column\":2,\"message\":\"first\"}]}\n\
{\"file\":\"lib/b.nix\",\"results\":[{\"line\":2,\"column\":3,\"message\":\"second\"}]}\n";
		let out = parse(stdout, b"", 0);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(got, [("first", Some("a.nix")), ("second", Some("lib/b.nix"))]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: tests::SPANS,
		stderr: b"",
		exit: 0,
	},
	crate::contract::Sample {
		stdout: tests::SPANS,
		stderr: b"",
		exit: 1,
	},
];
