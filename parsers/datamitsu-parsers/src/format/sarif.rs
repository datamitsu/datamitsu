//! SARIF 2.1.0: a JSON object with `version` and a `runs` array.
//!
//! One finding per location of each result (a result without one gives a
//! finding without a file). The rule is `ruleId`, or the id of the rule
//! `rule.index`/`ruleIndex` points at, and its `helpUri` is the URL. The level
//! is the result's `level`, or its rule's `defaultConfiguration.level`; `none`
//! and an absent level give none. Fingerprints are ignored: the core computes
//! its own. A URI is read as the path it names — percent-decoded, `file:`
//! stripped — and a relative one stays relative, whatever base it names.
use tinyjson::JsonValue;

use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::json_diag::{at, elements, member, position, text};
use crate::response::Response;
use crate::severity::{self, Level};

const LEVELS: &[Level] = &[
	Level("error", severity::ERROR),
	Level("warning", severity::WARNING),
	Level("note", severity::INFO),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "sarif",
	description: "SARIF 2.1.0, the static-analysis interchange format: `ruff check --output-format sarif`, \
        `semgrep --sarif`, `checkstyle -f sarif`, `trivy fs --format sarif`. Recognized by a JSON object \
        with `version` and a `runs` array, findings or not.",
	url: "https://docs.oasis-open.org/sarif/sarif/v2.1.0/sarif-v2.1.0.html",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	super::each_stream(DESCRIPTOR.name, stdout, stderr, |s| {
		crate::json_diag::find_envelope(s, from_log)
	})
}

fn from_log(log: &JsonValue) -> Option<Vec<RawDiagnostic>> {
	member(log, "version").and_then(text)?;
	let runs = member(log, "runs").and_then(elements)?;
	let mut out = Vec::new();
	for run in runs {
		let driver = at(run, &["tool", "driver"]);
		let rules = driver.and_then(|d| member(d, "rules")).and_then(elements);
		for result in member(run, "results").and_then(elements).into_iter().flatten() {
			findings(result, driver, rules, &mut out);
		}
	}
	Some(out)
}

fn findings(
	result: &JsonValue,
	driver: Option<&JsonValue>,
	rules: Option<&Vec<JsonValue>>,
	out: &mut Vec<RawDiagnostic>,
) {
	let rule = rule_of(result, rules);
	let Some(message) = message_of(result, rule, driver) else {
		return;
	};
	let code = member(result, "ruleId")
		.and_then(text)
		.or_else(|| rule.and_then(|r| member(r, "id")).and_then(text))
		.map(str::to_string);
	let url = rule
		.and_then(|r| member(r, "helpUri"))
		.and_then(text)
		.map(str::to_string);
	let level = member(result, "level")
		.or_else(|| rule.and_then(|r| at(r, &["defaultConfiguration", "level"])))
		.and_then(text)
		.and_then(|l| severity::of(LEVELS, l));
	let base = RawDiagnostic {
		message: message.to_string(),
		code,
		url,
		severity: level,
		..RawDiagnostic::default()
	};
	let locations = member(result, "locations").and_then(elements).filter(|l| !l.is_empty());
	let Some(locations) = locations else {
		out.push(base);
		return;
	};
	for location in locations {
		let physical = member(location, "physicalLocation");
		let region = physical.and_then(|p| member(p, "region"));
		let num = |key| region.and_then(|r| member(r, key)).and_then(position);
		out.push(RawDiagnostic {
			row: num("startLine"),
			col: num("startColumn"),
			end_row: num("endLine"),
			end_col: num("endColumn"),
			file: physical
				.and_then(|p| at(p, &["artifactLocation", "uri"]))
				.and_then(text)
				.and_then(uri_path)
				.as_deref()
				.and_then(crate::diagnostic::file_field),
			..base.clone()
		});
	}
}

/// A result's message: its text (or markdown), or the string its `id` names
/// among the rule's `messageStrings` or the driver's `globalMessageStrings`,
/// with each `{n}` replaced by the n-th of its `arguments`.
fn message_of(result: &JsonValue, rule: Option<&JsonValue>, driver: Option<&JsonValue>) -> Option<String> {
	let message = member(result, "message")?;
	let template = match message_text(message) {
		Some(t) => t,
		None => {
			let id = member(message, "id").and_then(text)?;
			named(rule, "messageStrings", id).or_else(|| named(driver, "globalMessageStrings", id))?
		}
	};
	Some(match member(message, "arguments").and_then(elements) {
		Some(arguments) => placeholders(template, arguments),
		None => template.to_string(),
	})
}

fn message_text(message: &JsonValue) -> Option<&str> {
	member(message, "text")
		.or_else(|| member(message, "markdown"))
		.and_then(text)
}

/// The message string `id` among `owner`'s `key` table.
fn named<'a>(owner: Option<&'a JsonValue>, key: &str, id: &str) -> Option<&'a str> {
	message_text(member(member(owner?, key)?, id)?)
}

/// `template` with `{n}` replaced by the n-th argument and `{{`, `}}` by one
/// brace (SARIF §3.11.5); a placeholder without its argument stays as written.
fn placeholders(template: &str, arguments: &[JsonValue]) -> String {
	let mut out = String::with_capacity(template.len());
	let mut rest = template;
	while let Some(i) = rest.find(['{', '}']) {
		out.push_str(&rest[..i]);
		let tail = &rest[i..];
		if tail.starts_with("{{") || tail.starts_with("}}") {
			out.push_str(&tail[..1]);
			rest = &tail[2..];
			continue;
		}
		let argument = tail
			.strip_prefix('{')
			.and_then(|t| t.find('}').map(|close| &t[..close]))
			.filter(|n| !n.is_empty() && n.bytes().all(|b| b.is_ascii_digit()))
			.and_then(|n| Some((n.len(), arguments.get(n.parse::<usize>().ok()?).and_then(text)?)));
		match argument {
			Some((len, value)) => {
				out.push_str(value);
				rest = &tail[len + 2..];
			}
			None => {
				out.push_str(&tail[..1]);
				rest = &tail[1..];
			}
		}
	}
	out.push_str(rest);
	out
}

/// The rule a result names by index, or by id among the driver's rules.
fn rule_of<'a>(result: &'a JsonValue, rules: Option<&'a Vec<JsonValue>>) -> Option<&'a JsonValue> {
	let rules = rules?;
	let index = member(result, "ruleIndex")
		.or_else(|| at(result, &["rule", "index"]))
		.and_then(position);
	if let Some(rule) = index.and_then(|i| rules.get(i as usize)) {
		return Some(rule);
	}
	let id = member(result, "ruleId").and_then(text)?;
	rules.iter().find(|r| member(r, "id").and_then(text) == Some(id))
}

/// The path a SARIF artifact URI names: percent-decoded, with a `file:`
/// scheme and the `/` before a drive letter removed. A URI of another scheme
/// names no local file.
fn uri_path(uri: &str) -> Option<String> {
	let path = match uri.strip_prefix("file:") {
		Some(rest) => {
			let path = match rest.strip_prefix("//") {
				Some(after) if after.starts_with('/') => after,
				Some(after) if after.starts_with("localhost/") => &after["localhost".len()..],
				Some(after) => return Some(format!("//{}", percent_decode(after))),
				None => rest,
			};
			let b = path.as_bytes();
			if b.len() >= 3 && b[0] == b'/' && b[1].is_ascii_alphabetic() && b[2] == b':' {
				&path[1..]
			} else {
				path
			}
		}
		None if uri.contains("://") => return None,
		None => uri,
	};
	Some(percent_decode(path))
}

fn percent_decode(s: &str) -> String {
	let bytes = s.as_bytes();
	let mut out = Vec::with_capacity(bytes.len());
	let mut i = 0;
	while i < bytes.len() {
		let hex = |b: u8| (b as char).to_digit(16);
		match (
			bytes[i],
			bytes.get(i + 1).copied().and_then(hex),
			bytes.get(i + 2).copied().and_then(hex),
		) {
			(b'%', Some(hi), Some(lo)) => {
				out.push((hi * 16 + lo) as u8);
				i += 3;
			}
			(b, _, _) => {
				out.push(b);
				i += 1;
			}
		}
	}
	String::from_utf8_lossy(&out).into_owned()
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const RUFF: &[u8] = br#"{
  "$schema": "https://json.schemastore.org/sarif-2.1.0.json",
  "runs": [
    {
      "results": [
        {
          "level": "error",
          "locations": [
            {
              "physicalLocation": {
                "artifactLocation": { "uri": "file:///work/src/a%20b.py" },
                "region": { "endColumn": 13, "endLine": 1, "startColumn": 8, "startLine": 1 }
              }
            }
          ],
          "message": { "text": "`os` imported but unused" },
          "ruleId": "F401"
        }
      ],
      "tool": {
        "driver": {
          "name": "ruff",
          "rules": [
            { "id": "F401", "helpUri": "https://docs.astral.sh/ruff/rules/unused-import" }
          ]
        }
      }
    }
  ],
  "version": "2.1.0"
}"#;

	#[test]
	fn reads_a_result_with_its_rule() {
		let r = parse(RUFF, b"", 1);
		assert!(r.recognized);
		assert_eq!(r.format, "sarif");
		let d = &r.diagnostics[0];
		assert_eq!(d.file.as_deref(), Some("/work/src/a b.py"));
		assert_eq!(
			(d.row, d.col, d.end_row, d.end_col),
			(Some(1), Some(8), Some(1), Some(13))
		);
		assert_eq!(d.code.as_deref(), Some("F401"));
		assert_eq!(
			d.url.as_deref(),
			Some("https://docs.astral.sh/ruff/rules/unused-import")
		);
		assert_eq!(d.severity, Some(severity::ERROR));
	}

	#[test]
	fn a_clean_log_is_recognized_and_empty() {
		let r = parse(
			br#"{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[]}]}"#,
			b"",
			0,
		);
		assert!(r.recognized && r.diagnostics.is_empty());
		let r = parse(br#"{"version":"2.1.0","runs":[]}"#, b"", 0);
		assert!(r.recognized && r.diagnostics.is_empty());
	}

	#[test]
	fn the_rule_by_index_the_default_level_and_no_level() {
		let log = br#"{"version":"2.1.0","runs":[{"tool":{"driver":{"rules":[
          {"id":"R0"},{"id":"R1","defaultConfiguration":{"level":"note"}}]}},
          "results":[{"ruleIndex":1,"message":{"text":"a"}},{"message":{"text":"b"},"level":"none"}]}]}"#;
		let r = parse(log, b"", 0);
		assert_eq!(r.diagnostics.len(), 2);
		assert_eq!(r.diagnostics[0].code.as_deref(), Some("R1"));
		assert_eq!(r.diagnostics[0].severity, Some(severity::INFO));
		assert_eq!(r.diagnostics[0].file, None);
		assert_eq!(r.diagnostics[1].severity, None);
	}

	#[test]
	fn a_message_given_by_id_is_looked_up() {
		let log = br#"{"version":"2.1.0","runs":[{"tool":{"driver":{
			"globalMessageStrings":{"G":{"text":"global {0}"}},
			"rules":[{"id":"R1","messageStrings":{"M":{"text":"'{0}' is unused in {1}; {{literal}} {2}"}}}]}},
			"results":[
				{"ruleId":"R1","message":{"id":"M","arguments":["x","f"]}},
				{"ruleId":"R1","message":{"id":"G","arguments":["y"]}},
				{"ruleId":"R1","message":{"id":"missing"}}]}]}"#;
		let r = parse(log, b"", 0);
		let messages: Vec<_> = r.diagnostics.iter().map(|d| d.message.as_str()).collect();
		assert_eq!(messages, ["'x' is unused in f; {literal} {2}", "global y"]);
	}

	#[test]
	fn reads_the_log_out_of_noise_on_either_stream() {
		let mut noisy = b"warning: config deprecated {see docs}\n".to_vec();
		noisy.extend_from_slice(RUFF);
		noisy.extend_from_slice(b"\nFound 1 error.\n");
		assert_eq!(parse(b"", &noisy, 1).diagnostics.len(), 1);
	}

	#[test]
	fn not_a_log() {
		for out in [
			&b"{}"[..],
			b"[]",
			br#"{"runs":[]}"#,
			br#"{"version":"2.1.0"}"#,
			br#"{"version":"2.1.0","runs":{}}"#,
			b"plain text",
			br#"{"version":"2.1.0","runs":[{"results":[{"message":{"text":"cut"#,
		] {
			assert!(!parse(out, b"", 1).recognized, "{}", String::from_utf8_lossy(out));
		}
	}

	#[test]
	fn a_uri_names_its_path() {
		assert_eq!(uri_path("file:///C:/x/a.c").as_deref(), Some("C:/x/a.c"));
		assert_eq!(
			uri_path("file://server/share/a.c").as_deref(),
			Some("//server/share/a.c")
		);
		assert_eq!(uri_path("src/a%2Bb.ts").as_deref(), Some("src/a+b.ts"));
		assert_eq!(uri_path("file:relative.txt").as_deref(), Some("relative.txt"));
		assert_eq!(uri_path("https://example.test/a"), None);
		assert_eq!(uri_path("100%").as_deref(), Some("100%"));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: tests::RUFF,
	stderr: b"",
	exit: 1,
}];
