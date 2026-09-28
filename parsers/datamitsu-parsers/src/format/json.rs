//! The plain JSON shape none-ls reads by default: an array whose every element
//! is an object with a `message` and a `line`, and optionally `column`,
//! `endLine`, `endColumn`, `ruleId`, `level` and `file`.
use tinyjson::JsonValue;

use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::json_diag::{self, elements, member, Attrs};
use crate::response::Response;
use crate::severity::{self, Level};

const LEVELS: &[Level] = &[
	Level("error", severity::ERROR),
	Level("warning", severity::WARNING),
	Level("info", severity::INFO),
	Level("information", severity::INFO),
	Level("hint", severity::HINT),
];

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "json",
	description: "The plain JSON array none-ls reads by default: objects with `message`, `line`, `column`, \
        `endLine`, `endColumn`, `ruleId`, `level` and `file`. Recognized by an array whose every element \
        carries `message` and `line`.",
	url: "",
	operations: &[],
	severities: LEVELS,
	column_unit: "",
	category: "",
	kind: "format",
};

pub fn parse(stdout: &[u8], stderr: &[u8], _exit_code: i32) -> Response {
	for stream in [stdout, stderr] {
		if let Some(diags) = json_diag::find_envelope(stream, from_array) {
			return Response::recognized(DESCRIPTOR.name, diags);
		}
	}
	Response::unrecognized(DESCRIPTOR.name)
}

fn from_array(v: &JsonValue) -> Option<Vec<RawDiagnostic>> {
	let items = elements(v).filter(|a| !a.is_empty())?;
	if !items
		.iter()
		.all(|i| member(i, "message").is_some() && member(i, "line").is_some())
	{
		return None;
	}
	let attrs = Attrs::defaults();
	Some(
		items
			.iter()
			.filter_map(|i| json_diag::from_obj(i, &attrs, |l| severity::of(LEVELS, l)))
			.collect(),
	)
}

#[cfg(test)]
mod tests {
	use super::*;

	pub(super) const REPORT: &[u8] = br#"[{"message":"trailing spaces","line":3,"column":7,"ruleId":"trailing-spaces","level":"warning","file":"a.yaml"},{"message":"too long","line":9,"level":"fatal"}]"#;

	#[test]
	fn reads_the_default_attributes() {
		let r = parse(REPORT, b"", 1);
		assert!(r.recognized);
		let d = &r.diagnostics[0];
		assert_eq!((d.file.as_deref(), d.row, d.col), (Some("a.yaml"), Some(3), Some(7)));
		assert_eq!(
			(d.code.as_deref(), d.severity),
			(Some("trailing-spaces"), Some(severity::WARNING))
		);
		assert_eq!(r.diagnostics[1].severity, None);
	}

	#[test]
	fn not_the_shape() {
		for out in [
			&b"[]"[..],
			b"{}",
			br#"[{"message":"no line"}]"#,
			br#"{"message":"m","line":1}"#,
			br#"[{"message":"m","line":1}"#,
		] {
			assert!(!parse(out, b"", 1).recognized, "{}", String::from_utf8_lossy(out));
		}
	}
}

/// Recorded or representative outputs every parser check runs over (`crate::contract`).
#[cfg(test)]
pub(crate) const SAMPLES: &[crate::contract::Sample] = &[crate::contract::Sample {
	stdout: tests::REPORT,
	stderr: b"",
	exit: 1,
}];
