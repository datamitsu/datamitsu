//! GitHub workflow commands: `::error file=a.py,line=3,col=5,title=E501::message`.
//!
//! A line is one when, after leading space, it starts with `::error`,
//! `::warning` or `::notice`, optionally followed by a space and properties,
//! then `::` and the message. Values and the message are unescaped the way the
//! runner unescapes them (`%25`, `%0D`, `%0A`, and in values `%3A` and `%2C`).
use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::response::Response;
use crate::severity::{self, Level};

use super::lines;

const LEVELS: &[Level] = &[
	Level("error", severity::ERROR),
	Level("warning", severity::WARNING),
	Level("notice", severity::INFO),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "github-annotations",
	description: "GitHub workflow commands `::error file=…,line=…,col=…,endLine=…,endColumn=…,title=…::message`, \
        as `ruff check --output-format github` and `yamllint -f github` print them; `title` becomes the code. \
        Recognized when a line matches.",
	url: "https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-commands",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	let diags: Vec<RawDiagnostic> = lines::of(stdout, stderr).iter().filter_map(|l| parse_line(l)).collect();
	if diags.is_empty() {
		return Response::unrecognized(DESCRIPTOR.name);
	}
	Response::recognized(DESCRIPTOR.name, diags)
}

fn parse_line(line: &str) -> Option<RawDiagnostic> {
	let command = line.trim_start().strip_prefix("::")?;
	let level = LEVELS.iter().find(|l| {
		command
			.strip_prefix(l.0)
			.is_some_and(|after| after.starts_with(' ') || after.starts_with("::"))
	})?;
	let after = &command[level.0.len()..];
	let (props, message) = after.split_once("::")?;
	let mut d = RawDiagnostic {
		message: unescape(message, false),
		severity: severity::of(LEVELS, level.0),
		..RawDiagnostic::default()
	};
	for prop in props.trim_start().split(',').filter(|p| !p.is_empty()) {
		let Some((key, value)) = prop.split_once('=') else {
			continue;
		};
		let value = unescape(value, true);
		match key.trim() {
			"file" => d.file = crate::diagnostic::file_field(&value),
			"line" => d.row = value.parse().ok(),
			"col" => d.col = value.parse().ok(),
			"endLine" => d.end_row = value.parse().ok(),
			"endColumn" => d.end_col = value.parse().ok(),
			"title" if !value.is_empty() => d.code = Some(value),
			_ => {}
		}
	}
	Some(d)
}

fn unescape(s: &str, property: bool) -> String {
	let mut out = s.replace("%0D", "\r").replace("%0A", "\n");
	if property {
		out = out.replace("%3A", ":").replace("%2C", ",");
	}
	out.replace("%25", "%")
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn reads_every_property() {
		let r = parse(
			b"::error file=src/a%2Cb.py,line=3,col=5,endLine=3,endColumn=9,title=E501::Line too long%0Asecond%25\n",
			b"",
			1,
		);
		assert!(r.recognized);
		let d = &r.diagnostics[0];
		assert_eq!(d.file.as_deref(), Some("src/a,b.py"));
		assert_eq!(
			(d.row, d.col, d.end_row, d.end_col),
			(Some(3), Some(5), Some(3), Some(9))
		);
		assert_eq!(d.code.as_deref(), Some("E501"));
		assert_eq!(d.severity, Some(severity::ERROR));
		assert_eq!(d.message, "Line too long\nsecond%");
	}

	#[test]
	fn a_command_without_properties_and_indented_ones() {
		let r = parse(b"  ::warning::no file\n::notice title=n::fyi\n", b"", 0);
		assert_eq!(r.diagnostics.len(), 2);
		assert_eq!(
			(r.diagnostics[0].file.as_deref(), r.diagnostics[0].severity),
			(None, Some(severity::WARNING))
		);
		assert_eq!(r.diagnostics[1].severity, Some(severity::INFO));
	}

	#[test]
	fn other_commands_and_colons_in_json_are_no_finding() {
		let r = parse(
			b"::group::lint\n::add-mask::x\n{\"m\":\"::error::in a string\"}\nerror::x\n::errors::y\n",
			b"",
			1,
		);
		assert!(!r.recognized);
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: b"::error title=E501,file=a.py,line=1,col=89,endLine=1,endColumn=100::a.py:1:89: E501 Line too long (99 > 88)\n::warning file=b.yaml,line=2,col=1::[truthy] truthy value should be one of [false, true]\n",
	stderr: b"",
	exit: 1,
}];
