//! credo — static analysis of Elixir files for enforcing code consistency.
//! Ported from the none-ls diagnostics/credo builtin.
//!
//! credo emits a JSON object `{"issues":[…]}` (sometimes prefixed by elixir
//! compiler warnings). Each issue carries `message`, `check` (the rule),
//! `line_no`, optional `column` / `column_end` (1-based, the end naming the
//! column after the trigger), and a numeric `priority`. The priority is read as
//! credo's own class for it (`higher` > 19, `high` 10–19, `normal` 0–9, `low`
//! -10–-1, `ignore` below), and the class through the vocabulary. When no `{` is
//! found, or JSON decoding fails, the whole output becomes one diagnostic with no
//! position and no level.

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity::{self, Level};

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "credo",
	description: "Static analysis of `elixir` files for enforcing code consistency.",
	url: "https://hexdocs.pm/credo",
	severities: &[
		Level("higher", severity::ERROR),
		Level("high", severity::ERROR),
		Level("normal", severity::WARNING),
		Level("low", severity::INFO),
		Level("ignore", severity::HINT),
	],
	column_unit: "",
	category: "",
	kind: "tool",
	operations: &[Operation {
		mode: "lint",
		args: &["credo", "suggest", "--format", "json", "--read-from-stdin", "{file}"],
		stdin: true,
	}],
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// credo is tricky: with no elixir warnings its own output is on stdout; with
	// elixir warnings its output stays on stdout while stderr holds the warnings.
	// When stdout is empty, fall back to stderr (mirrors `params.output or params.err`).
	let stdout = String::from_utf8_lossy(stdout);
	let stderr = String::from_utf8_lossy(stderr);
	let output = if stdout.trim().is_empty() {
		stderr.as_ref()
	} else {
		stdout.as_ref()
	};

	if output.trim().is_empty() {
		return Vec::new();
	}

	// Slice from the first `{` — credo may prefix the JSON with compiler warnings.
	let Some(json_index) = output.find('{') else {
		return vec![generic_issue(output)];
	};
	let maybe_json = &output[json_index..];

	let decoded: JsonValue = match maybe_json.parse() {
		Ok(v) => v,
		Err(_) => return vec![generic_issue(output)],
	};

	let issues = match &decoded {
		JsonValue::Object(map) => match map.get("issues") {
			Some(JsonValue::Array(items)) => items,
			_ => return Vec::new(),
		},
		_ => return Vec::new(),
	};

	issues.iter().filter_map(from_issue).collect()
}

fn from_issue(issue: &JsonValue) -> Option<RawDiagnostic> {
	let map = match issue {
		JsonValue::Object(m) => m,
		_ => return None,
	};
	let message = match map.get("message") {
		Some(JsonValue::String(s)) => s.clone(),
		_ => return None,
	};
	Some(RawDiagnostic {
		message,
		row: get_u32(map, "line_no"),
		col: get_u32(map, "column"),
		end_col: get_u32(map, "column_end"),
		severity: severity_of(map),
		source: Some("credo".to_string()),
		code: match map.get("check") {
			Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
			_ => None,
		},
		file: match map.get("filename") {
			Some(JsonValue::String(s)) => crate::diagnostic::file_field(s),
			_ => None,
		},
		..RawDiagnostic::default()
	})
}

/// credo's numeric `priority`, read as the priority class credo names it by.
fn severity_of(map: &std::collections::HashMap<String, JsonValue>) -> Option<u8> {
	let priority = match map.get("priority") {
		Some(JsonValue::Number(n)) if n.is_finite() => *n,
		_ => return None,
	};
	let class = if priority > 19.0 {
		"higher"
	} else if priority >= 10.0 {
		"high"
	} else if priority >= 0.0 {
		"normal"
	} else if priority >= -10.0 {
		"low"
	} else {
		"ignore"
	};
	severity::of(DESCRIPTOR.severities, class)
}

fn get_u32(map: &std::collections::HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match map.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

fn generic_issue(output: &str) -> RawDiagnostic {
	RawDiagnostic {
		message: output.to_string(),
		source: Some("credo".to_string()),
		..RawDiagnostic::default()
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn parses_issues_with_priority_severity() {
		let json = br#"{"issues":[
            {"check":"Credo.Check.Refactor.PipeChainStart","message":"Pipe chain should start with a raw value.","line_no":12,"column":5,"column_end":20,"priority":11},
            {"message":"Modules should have a @moduledoc tag.","line_no":1,"priority":1},
            {"message":"low prio note","line_no":3,"priority":-5}
        ]}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 3);
		assert_eq!(out[0].message, "Pipe chain should start with a raw value.");
		assert_eq!(out[0].row, Some(12));
		assert_eq!(out[0].col, Some(5));
		assert_eq!(out[0].end_col, Some(20));
		assert_eq!(out[0].severity, Some(severity::ERROR));
		assert_eq!(out[0].source.as_deref(), Some("credo"));
		assert_eq!(out[0].code.as_deref(), Some("Credo.Check.Refactor.PipeChainStart"));
		assert_eq!(out[1].severity, Some(severity::WARNING));
		assert_eq!(out[1].col, None);
		assert_eq!(out[1].code, None);
		assert_eq!(out[2].severity, Some(severity::INFO));
	}

	#[test]
	fn reads_every_priority_class() {
		for (priority, want) in [
			(20, severity::ERROR),
			(10, severity::ERROR),
			(9, severity::WARNING),
			(0, severity::WARNING),
			(-1, severity::INFO),
			(-10, severity::INFO),
			(-11, severity::HINT),
		] {
			let json = format!(r#"{{"issues":[{{"message":"m","line_no":1,"priority":{priority}}}]}}"#);
			assert_eq!(parse(json.as_bytes(), b"", 0)[0].severity, Some(want), "{priority}");
		}
	}

	#[test]
	fn an_issue_without_a_priority_has_no_level() {
		let out = parse(br#"{"issues":[{"message":"m","line_no":1}]}"#, b"", 0);
		assert_eq!(out[0].severity, None);
	}

	#[test]
	fn skips_compiler_warnings_before_json() {
		let json = br#"warning: variable unused
{"issues":[{"message":"x","line_no":2,"priority":0}]}"#;
		let out = parse(json, b"", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].message, "x");
		assert_eq!(out[0].severity, Some(severity::WARNING));
	}

	#[test]
	fn non_json_output_becomes_one_issue_without_position_or_level() {
		let out = parse(b"", b"** (Mix) the task could not be found", 0);
		assert_eq!(out.len(), 1);
		assert_eq!(out[0].row, None);
		assert_eq!(out[0].severity, None);
		assert!(out[0].message.contains("could not be found"));
	}

	#[test]
	fn each_issue_names_its_file() {
		let json = br#"{"issues":[
            {"message":"first","filename":"lib/a.ex","line_no":1,"priority":1},
            {"message":"second","filename":"lib/b.ex","line_no":2,"priority":1}]}"#;
		let out = parse(json, b"", 2);
		let got: Vec<_> = out.iter().map(|d| (d.message.as_str(), d.file.as_deref())).collect();
		assert_eq!(got, [("first", Some("lib/a.ex")), ("second", Some("lib/b.ex"))]);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[
	crate::contract::Sample {
		stdout: br##"{"issues":[{"category":"readability","check":"Credo.Check.Readability.ModuleDoc","column":11,"column_end":14,"filename":"lib/app.ex","line_no":1,"message":"Modules should have a @moduledoc tag.","priority":1,"scope":"App","trigger":"App"},{"category":"refactor","check":"Credo.Check.Refactor.PipeChainStart","column":5,"column_end":7,"filename":"lib/app.ex","line_no":12,"message":"Pipe chain should start with a raw value.","priority":11,"scope":"App.run","trigger":"|>"},{"category":"design","check":"Credo.Check.Design.TagTODO","column":null,"column_end":null,"filename":"lib/app.ex","line_no":20,"message":"Found a TODO tag in a comment: # TODO: tidy","priority":-5,"scope":"App.run","trigger":"# TODO: tidy"}]}"##,
		stderr: b"",
		exit: 12,
	},
	crate::contract::Sample {
		stdout: b"",
		stderr: b"** (Mix) The task \"credo\" could not be found\n",
		exit: 1,
	},
];
