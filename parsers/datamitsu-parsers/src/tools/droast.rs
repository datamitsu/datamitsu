//! droast (dockerfile-roast) — an opinionated Dockerfile linter.
//!
//! Ported from `droast --format json`, whose shape depends on how many
//! Dockerfiles the run linted: one yields a single result object, several yield
//! an array of them. A repository scan (`droast .`) is usually several.
//!
//! ```json
//! {"file":"Dockerfile","total":1,"errors":0,"warnings":1,"infos":0,
//!  "findings":[{"rule":"DF001","severity":"WARN","line":1,"column":1,
//!               "end_line":1,"end_column":12,"message":"…","roast":"…"}]}
//! ```
//!
//! What makes it more than a `json_diag::from_json` call:
//!   * findings nest under their file's result, and the file is named only there;
//!   * `line` is `0` for a finding about the whole file (a missing `HEALTHCHECK`,
//!     no `EXPOSE`), which droast itself renders without a position — so `0`
//!     means absent here, never line zero. `column` and the end fields are
//!     omitted rather than zeroed, and `end_column` is already exclusive;
//!   * the level is `ERROR`/`WARN`/`INFO`, not the lowercase words the shared
//!     helper expects.
//!
//! `message` is the technical text. `roast` is the joke droast prints beside it
//! and is present even under `--no-roast`, so it is never read.
//!
//! ShellCheck findings from droast's bridge arrive in the same array with their
//! own `SC####` rule ids, and need nothing special.
//!
//! droast also writes to stderr, and that is parsed too: a Dockerfile it could
//! not read is reported only there (`x Failed to read '…'`) and is missing from
//! the JSON, and discovery problems — a Compose file that does not parse, a Bake
//! target with a missing parent — are `!` warnings. The core shows parsed
//! diagnostics in place of the raw output, so leaving these out would hide them
//! whenever any other file produced a finding.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "droast",
	description: "An opinionated Dockerfile linter with governed suppressions, build-context and ShellCheck awareness.",
	url: "https://github.com/immanuwell/dockerfile-roast",
	operations: &[Operation {
		mode: "lint",
		args: &["--format", "json", "--no-roast"],
		stdin: false,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	let mut out = crate::tools::json_diag::extract_lenient(stdout, from_report);
	out.extend(from_stderr(stderr));
	out
}

fn from_report(value: &JsonValue) -> Vec<RawDiagnostic> {
	match value {
		JsonValue::Array(results) => results.iter().flat_map(from_result).collect(),
		JsonValue::Object(_) => from_result(value),
		_ => Vec::new(),
	}
}

fn from_result(value: &JsonValue) -> Vec<RawDiagnostic> {
	let JsonValue::Object(result) = value else {
		return Vec::new();
	};
	let Some(JsonValue::Array(findings)) = result.get("findings") else {
		return Vec::new();
	};
	let file = match result.get("file") {
		Some(JsonValue::String(s)) => crate::diagnostic::file_field(s),
		_ => None,
	};
	findings
		.iter()
		.filter_map(|finding| from_finding(finding, &file))
		.collect()
}

fn from_finding(value: &JsonValue, file: &Option<String>) -> Option<RawDiagnostic> {
	let JsonValue::Object(m) = value else {
		return None;
	};
	let message = string_field(m, "message")?;
	Some(RawDiagnostic {
		message,
		row: position(m, "line"),
		col: position(m, "column"),
		end_row: position(m, "end_line"),
		end_col: position(m, "end_column"),
		severity: string_field(m, "severity").and_then(|s| severity_of(&s)),
		code: string_field(m, "rule"),
		file: file.clone(),
		..RawDiagnostic::default()
	})
}

fn severity_of(level: &str) -> Option<u8> {
	match level.to_ascii_uppercase().as_str() {
		"ERROR" => Some(severity::ERROR),
		// `WARN` is what droast prints; `WARNING` is how its config and CLI spell
		// the same level, so a later release printing that would mean the same.
		"WARN" | "WARNING" => Some(severity::WARNING),
		"INFO" => Some(severity::INFO),
		_ => None,
	}
}

/// droast prefixes its own stderr lines with `x` for an error and `!` for a
/// warning. Anything else there — a panic, clap's usage text, `Error:` from a run
/// that failed outright — is left to the raw output the core falls back to.
fn from_stderr(stderr: &[u8]) -> Vec<RawDiagnostic> {
	let text = String::from_utf8_lossy(stderr);
	text
		.lines()
		.filter_map(|line| {
			let line = strip_ansi(line);
			let (level, message) = match line.strip_prefix("x ") {
				Some(rest) => (severity::ERROR, rest),
				None => (severity::WARNING, line.strip_prefix("! ")?),
			};
			let message = message.trim();
			(!message.is_empty()).then(|| RawDiagnostic {
				message: message.to_string(),
				severity: Some(level),
				..RawDiagnostic::default()
			})
		})
		.collect()
}

/// The markers are colored when droast is forced to color a piped stream
/// (`CLICOLOR_FORCE`), which would otherwise hide the prefix behind an escape.
fn strip_ansi(line: &str) -> String {
	let mut out = String::with_capacity(line.len());
	let mut chars = line.chars();
	while let Some(c) = chars.next() {
		if c == '\u{1b}' {
			if chars.next() == Some('[') {
				for c in chars.by_ref() {
					if c.is_ascii_alphabetic() {
						break;
					}
				}
			}
			continue;
		}
		out.push(c);
	}
	out
}

fn string_field(m: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match m.get(key) {
		Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
		_ => None,
	}
}

/// A 1-based position, where droast's `0` means "no position".
fn position(m: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match m.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n).filter(|&n| n > 0),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	// Verbatim `droast --format json --no-roast --shellcheck required .` over two
	// Dockerfiles (fingerprints and roasts shortened): DF and SC findings, a
	// whole-file finding at line 0, and a span with an exclusive end column.
	const REPOSITORY: &str = r#"[
  {
    "file": "Dockerfile",
    "total": 4,
    "errors": 1,
    "warnings": 2,
    "infos": 1,
    "findings": [
      {"rule": "DF036", "fingerprint": "sha256:e9ae", "severity": "INFO", "line": 0,
       "message": "No CMD or ENTRYPOINT declared in the final stage — the default command depends on the base image",
       "roast": "This stage inherits its default process from external image metadata."},
      {"rule": "DF001", "fingerprint": "sha256:4857", "severity": "WARN", "line": 1, "column": 1,
       "end_line": 1, "end_column": 12, "message": "'ubuntu' uses an unpinned image tag", "roast": "…"},
      {"rule": "SC2154", "fingerprint": "sha256:aa01", "severity": "WARN", "line": 2, "column": 36,
       "end_line": 2, "end_column": 40, "message": "foo is referenced but not assigned.", "roast": "…"},
      {"rule": "DF002", "fingerprint": "sha256:bb02", "severity": "ERROR", "line": 3, "column": 1,
       "end_line": 3, "end_column": 10, "message": "Container is explicitly set to run as root", "roast": "…"}
    ]
  },
  {
    "file": "svc/Dockerfile",
    "total": 1,
    "errors": 0,
    "warnings": 1,
    "infos": 0,
    "findings": [
      {"rule": "DF029", "fingerprint": "sha256:cc03", "severity": "WARN", "line": 2, "column": 1,
       "end_line": 2, "end_column": 17, "message": "apk add without --no-cache flag", "roast": "…"}
    ]
  }
]"#;

	// One Dockerfile: the result object on its own, not wrapped in an array.
	const SINGLE: &str = r#"{
  "file": "svc/Dockerfile",
  "total": 1,
  "errors": 0,
  "warnings": 0,
  "infos": 1,
  "findings": [
    {"rule": "DF012", "fingerprint": "sha256:4689", "severity": "INFO", "line": 0,
     "message": "No HEALTHCHECK defined", "roast": "No HEALTHCHECK? Your container is basically on the honor system."}
  ]
}"#;

	fn find<'a>(out: &'a [RawDiagnostic], code: &str) -> &'a RawDiagnostic {
		out
			.iter()
			.find(|d| d.code.as_deref() == Some(code))
			.unwrap_or_else(|| panic!("no diagnostic {code:?} in {out:#?}"))
	}

	#[test]
	fn reads_a_repository_scan_as_one_diagnostic_per_finding() {
		let out = parse(REPOSITORY.as_bytes(), b"", 1);

		assert_eq!(out.len(), 5);
		assert_eq!(find(&out, "DF001").file.as_deref(), Some("Dockerfile"));
		assert_eq!(find(&out, "DF029").file.as_deref(), Some("svc/Dockerfile"));
	}

	#[test]
	fn reads_a_single_dockerfile_report_that_is_not_an_array() {
		let out = parse(SINGLE.as_bytes(), b"", 0);

		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "No HEALTHCHECK defined");
		assert_eq!(out[0].file.as_deref(), Some("svc/Dockerfile"));
	}

	#[test]
	fn maps_droasts_uppercase_levels() {
		let out = parse(REPOSITORY.as_bytes(), b"", 1);

		assert_eq!(find(&out, "DF002").severity, Some(severity::ERROR));
		assert_eq!(find(&out, "DF001").severity, Some(severity::WARNING));
		assert_eq!(find(&out, "DF036").severity, Some(severity::INFO));
		// The CLI and config spelling of the same level, and an unknown one.
		let spelled = br#"{"file":"D","findings":[
            {"rule":"A","severity":"warning","line":1,"message":"a"},
            {"rule":"B","severity":"FATAL","line":1,"message":"b"}]}"#;
		let out = parse(spelled, b"", 1);
		assert_eq!(find(&out, "A").severity, Some(severity::WARNING));
		assert_eq!(find(&out, "B").severity, None);
	}

	#[test]
	fn a_whole_file_finding_carries_no_position() {
		// droast writes line 0 and omits the column for a finding about the file
		// as a whole. Line zero does not exist; the core places it instead.
		let diag = find(&parse(REPOSITORY.as_bytes(), b"", 1), "DF036").clone();

		assert_eq!(
			(diag.row, diag.col, diag.end_row, diag.end_col),
			(None, None, None, None)
		);
		assert_eq!(diag.file.as_deref(), Some("Dockerfile"));
	}

	#[test]
	fn keeps_the_span_droast_reports() {
		let diag = find(&parse(REPOSITORY.as_bytes(), b"", 1), "DF001").clone();

		assert_eq!((diag.row, diag.col), (Some(1), Some(1)));
		// Already exclusive, as the core expects: passed through untouched.
		assert_eq!((diag.end_row, diag.end_col), (Some(1), Some(12)));
	}

	#[test]
	fn reports_shellcheck_findings_under_their_own_codes() {
		let diag = find(&parse(REPOSITORY.as_bytes(), b"", 1), "SC2154").clone();

		assert_eq!(diag.message, "foo is referenced but not assigned.");
		assert_eq!((diag.row, diag.col), (Some(2), Some(36)));
		assert_eq!(diag.severity, Some(severity::WARNING));
	}

	#[test]
	fn never_reports_the_roast() {
		let out = parse(SINGLE.as_bytes(), b"", 0);

		assert!(!out[0].message.contains("honor system"));
	}

	#[test]
	fn surfaces_a_dockerfile_droast_could_not_read() {
		// Verbatim: an unreadable Dockerfile is reported only on stderr and is
		// absent from the JSON, so the stdout findings alone would hide it.
		let out = parse(SINGLE.as_bytes(), b"x Failed to read 'broken/Dockerfile'\n", 1);

		assert_eq!(out.len(), 2);
		let unread = out.iter().find(|d| d.code.is_none()).unwrap();
		assert_eq!(unread.message, "Failed to read 'broken/Dockerfile'");
		assert_eq!(unread.severity, Some(severity::ERROR));
		// The path is inside the sentence; lifting it out would be a guess.
		assert_eq!(unread.file, None);
	}

	#[test]
	fn surfaces_discovery_warnings() {
		let out = parse(
			b"",
			b"! Compose service \"api\" in 'compose.yaml' has an unresolved Dockerfile path\n",
			0,
		);

		assert_eq!(out.len(), 1);
		assert_eq!(
			out[0].message,
			"Compose service \"api\" in 'compose.yaml' has an unresolved Dockerfile path"
		);
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn reads_stderr_markers_through_forced_color() {
		let out = parse(b"", b"\x1b[31m\x1b[1mx\x1b[0m Failed to read 'Dockerfile'\r\n", 1);

		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "Failed to read 'Dockerfile'");
	}

	#[test]
	fn leaves_other_stderr_to_the_raw_output() {
		for stderr in [
			&b"Error: unknown key `fail_on` in droast.toml"[..],
			b"thread 'main' panicked at src/main.rs:1:1",
			b"x",
			b"x   ",
			b"exit code 1",
		] {
			assert!(
				parse(b"", stderr, 1).is_empty(),
				"stderr {:?}",
				String::from_utf8_lossy(stderr)
			);
		}
	}

	#[test]
	fn reports_nothing_for_a_clean_run() {
		assert!(parse(br#"{"file":"Dockerfile","total":0,"findings":[]}"#, b"", 0).is_empty());
		assert!(parse(br#"[{"file":"a","findings":[]},{"file":"b","findings":[]}]"#, b"", 0).is_empty());
	}

	#[test]
	fn yields_nothing_rather_than_guessing_on_malformed_input() {
		for input in [
			&b""[..],
			b"not json at all",
			br#"{"file":"Dockerfile","findings":"#,
			br#"{"file":"Dockerfile","findings":{}}"#,
			br#"["Dockerfile",42,null]"#,
			br#"{"file":"Dockerfile","findings":[{"rule":"DF001","line":1}]}"#, // no message
			br#"{"file":"Dockerfile","findings":[{"rule":"DF001","message":""}]}"#,
		] {
			assert!(
				parse(input, b"", 1).is_empty(),
				"input {:?}",
				String::from_utf8_lossy(input)
			);
		}
	}

	#[test]
	fn drops_a_position_that_is_not_a_usable_line_number() {
		for position in [r#""7""#, "-1", "null", "99999999999", "true", "0"] {
			let json = format!(r#"{{"file":"D","findings":[{{"rule":"R","line":{position},"message":"m"}}]}}"#);
			let out = parse(json.as_bytes(), b"", 1);
			assert_eq!(out.len(), 1, "line {position}");
			assert_eq!(out[0].row, None, "line {position}");
		}
	}

	#[test]
	fn keeps_a_finding_whose_result_names_no_file() {
		for result in [
			r#"{"findings":[{"rule":"R","message":"m"}]}"#,
			r#"{"file":"","findings":[{"rule":"R","message":"m"}]}"#,
			r#"{"file":"-","findings":[{"rule":"R","message":"m"}]}"#, // droast linting stdin
		] {
			let out = parse(result.as_bytes(), b"", 1);
			assert_eq!(out.len(), 1, "result {result}");
			assert_eq!(out[0].file, None, "result {result}");
		}
	}

	#[test]
	fn survives_noise_around_the_report() {
		let noisy = format!("warming up\n{}\ndone", REPOSITORY);

		assert_eq!(parse(noisy.as_bytes(), b"", 1).len(), 5);
	}

	#[test]
	fn carries_non_ascii_paths_and_messages_through_intact() {
		let out = parse(
			r#"{"file":"café/Dockerfile","findings":[{"rule":"DF009","severity":"WARN","line":2,"message":"WORKDIR 'naïve' is relative"}]}"#.as_bytes(),
			b"",
			1,
		);

		assert_eq!(out[0].file.as_deref(), Some("café/Dockerfile"));
		assert_eq!(out[0].message, "WORKDIR 'naïve' is relative");
	}
}
