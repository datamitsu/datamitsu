//! The module's self-description (`describe` export).
//!
//! Static counterpart to the dynamic `parse` dispatcher: it reports, as JSON,
//! which tools this module can parse, how each should be invoked to produce
//! parseable output, and the module's build-injected version. The Go core
//! aggregates and deduplicates these across all configured modules
//! (`datamitsu devtools parsers list`).
//!
//! **Version is the module's own truth, baked at build time like a Go ldflags
//! `-X`.** CI sets `DATAMITSU_PARSERS_VERSION`; absent it falls back to the crate
//! version so `describe` is never empty. The version is intentionally NOT a field
//! of the datamitsu `parsers` config entity — duplicating it there would let the
//! declared and actual versions drift. The config declares only url+hash.
//!
//! Each real tool owns its `DESCRIPTOR` in its `tools::<tool>` module, co-located
//! with that tool's parser, and is referenced from `tools::DESCRIPTORS`; the
//! format parsers' are in `format::DESCRIPTORS`.

use crate::diagnostic::json_string;
use crate::severity::Level;

/// Capabilities schema version. Bump on an incompatible shape change so the Go
/// decoder can refuse or adapt. Schema 2 adds each tool's level vocabulary,
/// column unit, category and kind, and with them the promise that a level comes
/// only from a token the tool printed. Schema 3 adds `abi`, the form `parse`
/// answers in, and `features`, the parsers this build carries.
const SCHEMA_VERSION: u32 = 3;

/// The form `parse` answers in: an object that says whether the parser
/// recognized the output (`crate::response`).
const ABI: u32 = 2;

/// The parser families this build carries: `tools` (one parser per tool) and
/// `format` (the standard formats and the sniffer).
fn features() -> Vec<&'static str> {
	let mut out = Vec::new();
	if cfg!(feature = "format") {
		out.push("format");
	}
	if cfg!(feature = "tools") {
		out.push("tools");
	}
	out
}

/// The build-injected module version. `DATAMITSU_PARSERS_VERSION` is read at
/// compile time (like an ldflags `-X`); when unset — a plain local `cargo build`
/// — the crate version from Cargo.toml stands in.
fn module_version() -> &'static str {
	option_env!("DATAMITSU_PARSERS_VERSION").unwrap_or(env!("CARGO_PKG_VERSION"))
}

/// The recommended invocation of a tool in one operation mode.
pub(crate) struct Operation {
	/// Operation the recipe is for (e.g. "lint").
	pub(crate) mode: &'static str,
	/// Args to pass the tool to produce output this parser understands.
	/// `{file}` is the per-file placeholder the core substitutes.
	pub(crate) args: &'static [&'static str],
	/// Whether the file content is fed on stdin rather than as a path arg.
	pub(crate) stdin: bool,
}

/// One tool this module knows how to parse.
pub(crate) struct ToolCapability {
	/// Dispatch name — the `parse` match arm and the value of `tool.outputParser`.
	pub(crate) name: &'static str,
	/// Human-readable description of the upstream tool.
	pub(crate) description: &'static str,
	/// Upstream tool URL, so a reader knows exactly what this parser targets.
	/// Empty for internal/pipe-test parsers.
	pub(crate) url: &'static str,
	/// Recommended invocations per mode; empty when the parser ships no canonical
	/// recipe yet.
	pub(crate) operations: &'static [Operation],
	/// The level tokens the tool prints and the level each maps to — the only
	/// source of a finding's severity ([`crate::severity::of`]). Empty for a
	/// tool that prints none.
	pub(crate) severities: &'static [Level],
	/// The unit the tool counts columns in — "utf-8" (bytes), "utf-16" (code
	/// units) or "utf-32" (code points) — measured on the tool; empty when
	/// it has not been measured (the tool is then on the module test's list of
	/// unknowns).
	pub(crate) column_unit: &'static str,
	/// "security" for a security scanner; empty otherwise.
	pub(crate) category: &'static str,
	/// What the parser reads: "tool" for the tool's own output format,
	/// "format" for a standard format any tool may print.
	pub(crate) kind: &'static str,
}

#[cfg(feature = "tools")]
fn tool_parsers() -> &'static [&'static ToolCapability] {
	crate::tools::DESCRIPTORS
}

#[cfg(not(feature = "tools"))]
fn tool_parsers() -> &'static [&'static ToolCapability] {
	&[]
}

#[cfg(feature = "format")]
fn format_parsers() -> &'static [&'static ToolCapability] {
	crate::format::DESCRIPTORS
}

#[cfg(not(feature = "format"))]
fn format_parsers() -> &'static [&'static ToolCapability] {
	&[]
}

/// Every parser this build describes: the tool parsers, then the format
/// parsers and the sniffer.
pub(crate) fn described() -> Vec<&'static ToolCapability> {
	tool_parsers().iter().chain(format_parsers()).copied().collect()
}

/// Serialize the module's full capability manifest to JSON.
pub fn describe_json() -> String {
	let tools: Vec<String> = described().into_iter().map(tool_json).collect();
	let features: Vec<String> = features().into_iter().map(json_string).collect();
	format!(
		r#"{{"schemaVersion":{},"module":{},"version":{},"abi":{},"features":[{}],"tools":[{}]}}"#,
		SCHEMA_VERSION,
		json_string("datamitsu-parsers"),
		json_string(module_version()),
		ABI,
		features.join(","),
		tools.join(","),
	)
}

fn tool_json(t: &ToolCapability) -> String {
	let ops: Vec<String> = t.operations.iter().map(operation_json).collect();
	let severities: Vec<String> = t.severities.iter().map(|l| json_string(l.0)).collect();
	let mut optional = String::new();
	if !t.column_unit.is_empty() {
		optional.push_str(&format!(r#","columnUnit":{}"#, json_string(t.column_unit)));
	}
	if !t.category.is_empty() {
		optional.push_str(&format!(r#","category":{}"#, json_string(t.category)));
	}
	format!(
		r#"{{"name":{},"description":{},"url":{},"operations":{{{}}},"severities":[{}]{},"kind":{}}}"#,
		json_string(t.name),
		json_string(t.description),
		json_string(t.url),
		ops.join(","),
		severities.join(","),
		optional,
		json_string(t.kind),
	)
}

fn operation_json(o: &Operation) -> String {
	let args: Vec<String> = o.args.iter().map(|a| json_string(a)).collect();
	format!(
		r#"{}:{{"args":[{}],"stdin":{}}}"#,
		json_string(o.mode),
		args.join(","),
		o.stdin,
	)
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn describe_advertises_schema_module_and_version() {
		let json = describe_json();
		assert!(json.contains(r#""schemaVersion":3"#), "json: {json}");
		assert!(json.contains(r#""module":"datamitsu-parsers""#), "json: {json}");
		assert!(json.contains(r#""abi":2"#), "json: {json}");
	}

	#[cfg(feature = "tools")]
	#[test]
	fn describe_names_both_features_of_the_public_build() {
		assert!(describe_json().contains(r#""features":["format","tools"]"#));
	}

	#[cfg(not(feature = "tools"))]
	#[test]
	fn describe_names_the_format_feature_alone_of_the_embedded_build() {
		let json = describe_json();
		assert!(json.contains(r#""features":["format"]"#), "json: {json}");
		assert!(!json.contains(r#""kind":"tool""#), "json: {json}");
	}

	#[test]
	fn describe_lists_every_format_parser_and_the_sniffer() {
		let json = describe_json();
		for name in ["sarif", "checkstyle-xml", "gcc", "fallback"] {
			assert!(json.contains(&format!(r#""name":"{name}""#)), "missing {name}: {json}");
		}
		assert!(json.contains(r#""kind":"format""#));
	}

	#[cfg(feature = "tools")]
	#[test]
	fn describe_lists_echo_and_real_tools() {
		let json = describe_json();
		for name in ["echo", "yamllint", "dotenv_linter", "cue_fmt"] {
			assert!(json.contains(&format!(r#""name":"{name}""#)), "missing {name}: {json}");
		}
	}

	#[cfg(feature = "tools")]
	#[test]
	fn describe_includes_an_invocation_recipe() {
		// yamllint advertises how to run it (parsable, stdin).
		let json = describe_json();
		assert!(
			json.contains(r#""lint":{"args":["--format","parsable","-"],"stdin":true}"#),
			"json: {json}"
		);
	}

	#[test]
	fn module_version_is_never_empty() {
		assert!(!module_version().is_empty());
	}
}
