//! Azure Pipelines logging commands:
//! `##vso[task.logissue type=error;sourcepath=a.py;linenumber=3;columnnumber=5;code=E501]message`.
//!
//! Properties are `key=value` pairs separated by `;`. Values and the message
//! are unescaped the way the agent unescapes them (`%AZP25`, `%3B`, `%0D`,
//! `%0A`, `%5D`).
use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::response::Response;
use crate::severity::{self, Level};

use super::lines;

const LEVELS: &[Level] = &[Level("error", severity::ERROR), Level("warning", severity::WARNING)];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "azure-logissue",
	description: "Azure Pipelines logging commands \
        `##vso[task.logissue type=…;sourcepath=…;linenumber=…;columnnumber=…;code=…]message`, as \
        `ruff check --output-format azure` prints them. Recognized when a line matches.",
	url: "https://learn.microsoft.com/en-us/azure/devops/pipelines/scripts/logging-commands",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

const PREFIX: &str = "##vso[task.logissue";

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	let diags: Vec<RawDiagnostic> = lines::of(stdout, stderr).iter().filter_map(|l| parse_line(l)).collect();
	if diags.is_empty() {
		return Response::unrecognized(DESCRIPTOR.name);
	}
	Response::recognized(DESCRIPTOR.name, diags)
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let rest = line.trim_start().strip_prefix(PREFIX)?;
	if !rest.starts_with([' ', ']']) {
		return None;
	}
	let (props, message) = rest.split_once(']')?;
	let mut d = RawDiagnostic {
		message: unescape(message),
		..RawDiagnostic::default()
	};
	let mut typed = false;
	for prop in props.split(';') {
		let Some((key, value)) = prop.trim().split_once('=') else {
			continue;
		};
		let value = unescape(value);
		match key {
			"type" => {
				typed = true;
				d.severity = severity::of(LEVELS, &value);
			}
			"sourcepath" => d.file = crate::diagnostic::file_field(&value),
			"linenumber" => d.row = value.parse().ok(),
			"columnnumber" => d.col = value.parse().ok(),
			"code" if !value.is_empty() => d.code = Some(value),
			_ => {}
		}
	}
	typed.then_some(d)
}

fn unescape(s: &str) -> String {
	s.replace("%3B", ";")
		.replace("%0D", "\r")
		.replace("%0A", "\n")
		.replace("%5D", "]")
		.replace("%AZP25", "%")
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn reads_every_property() {
		let r = parse(
			b"##vso[task.logissue type=error;sourcepath=src/a%3Bb.py;linenumber=3;columnnumber=5;code=E501;]Line too long%0Ab%AZP25\n",
			b"",
			1,
		);
		assert!(r.recognized);
		let d = &r.diagnostics[0];
		assert_eq!(d.file.as_deref(), Some("src/a;b.py"));
		assert_eq!((d.row, d.col), (Some(3), Some(5)));
		assert_eq!(d.code.as_deref(), Some("E501"));
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.message, "Line too long\nb%");
	}

	#[test]
	fn a_command_needs_a_type_and_the_logissue_task() {
		let r = parse(
			b"##vso[task.logissue sourcepath=a.py]no type\n##vso[task.setvariable variable=x]1\n##vso[task.logissuex type=error]y\n",
			b"",
			1,
		);
		assert!(!r.recognized);
		let r = parse(b"  ##vso[task.logissue type=warning]fyi\n", b"", 0);
		assert_eq!(r.diagnostics[0].severity, Some(severity::WARNING));
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"##vso[task.logissue type=error;sourcepath=a.py;linenumber=1;columnnumber=89;code=E501;]Line too long (99 > 88)\n##vso[task.logissue type=warning;sourcepath=b.py;linenumber=2;columnnumber=1;code=W291;]Trailing whitespace\n",
	stderr: b"",
	exit: 1,
}];
