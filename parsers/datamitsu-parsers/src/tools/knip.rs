//! knip — finds unused files, dependencies and exports.
//!
//! Ported from `knip --reporter json`, which groups findings by file rather than
//! listing them flat:
//!
//! ```json
//! {"issues":[{"file":"src/a.ts","exports":[{"name":"x","line":4,"col":14}],
//!             "files":[],"unlisted":[]}]}
//! ```
//!
//! Three things make it a bespoke parser rather than a `json_diag::from_json`
//! one-liner:
//!   * findings are **nested per issue type** under each file's entry, and the
//!     key is the only thing naming what is wrong — the report carries no message
//!     field, so this parser writes the sentence;
//!   * `duplicates` and `cycles` nest one level deeper, as arrays of groups, each
//!     group being the symbols that duplicate or the files in the cycle;
//!   * knip reports no severity at all, and `line`/`col` are absent for
//!     whole-file findings such as an unused file.
//!
//! Every finding is an error. knip's exit code does not distinguish between its
//! issue types — anything it reports fails the run — so leaving severity unset
//! would render a failing run as a page of warnings. Whether a kind of finding is
//! worth failing on is decided upstream, by which issue types the project
//! reports at all (knip's `rules`), not by a level the tool never emitted.
//!
//! The wording is knip's own, taken from its `ISSUE_TYPE_TITLE` singularized the
//! way its reporters do (`ies` → `y`, then a trailing `s` dropped), so a
//! diagnostic reads the same as the line knip would have printed. Two of those
//! titles say something other than "unused" and must not be paraphrased:
//! `optionalPeerDependencies` reports peers that **are** referenced, and
//! `catalogReferences` reports references that do not **resolve**.
//!
//! The issue type becomes the diagnostic `code`, so a reader sees which of knip's
//! checks fired without parsing the sentence.

use std::collections::HashMap;

use tinyjson::JsonValue;

use crate::capabilities::{Operation, ToolCapability};
use crate::diagnostic::RawDiagnostic;
use crate::severity;

pub const DESCRIPTOR: ToolCapability = ToolCapability {
	name: "knip",
	description: "Find unused files, dependencies and exports in JavaScript and TypeScript projects.",
	url: "https://knip.dev",
	operations: &[Operation {
		mode: "lint",
		args: &["--reporter", "json"],
		stdin: false,
	}],
};

/// knip's own title for one finding of each issue type, and whether the item's
/// name adds anything. `files` names the file it is already attached to, so its
/// name is dropped; every other type names a symbol, dependency or binary.
fn describe(issue_type: &str) -> Option<(&'static str, bool)> {
	Some(match issue_type {
		"binaries" => ("Unlisted binary", true),
		"catalog" => ("Unused catalog entry", true),
		"catalogReferences" => ("Unresolved catalog reference", true),
		"cycles" => ("Circular dependency", true),
		"dependencies" => ("Unused dependency", true),
		"devDependencies" => ("Unused devDependency", true),
		"duplicates" => ("Duplicate export", true),
		"enumMembers" => ("Unused exported enum member", true),
		"exports" => ("Unused export", true),
		"files" => ("Unused file", false),
		"namespaceMembers" => ("Unused exported namespace member", true),
		"nsExports" => ("Exports in used namespace", true),
		"nsTypes" => ("Exported types in used namespace", true),
		"optionalPeerDependencies" => ("Referenced optional peerDependency", true),
		"types" => ("Unused exported type", true),
		"unlisted" => ("Unlisted dependency", true),
		"unresolved" => ("Unresolved import", true),
		// "owners" is a CODEOWNERS annotation rather than a finding, and a later
		// knip may add types this build has never heard of.
		_ => return None,
	})
}

pub fn parse(stdout: &[u8], _stderr: &[u8], _exit_code: i32) -> Vec<RawDiagnostic> {
	// Lenient: knip writes its config-loading failures to stderr, but a plugin it
	// executes to read a config can print to stdout, which would otherwise cost
	// every finding in the run.
	crate::tools::json_diag::extract_lenient(stdout, from_report)
}

fn from_report(value: &JsonValue) -> Vec<RawDiagnostic> {
	let JsonValue::Object(root) = value else {
		return Vec::new();
	};
	let Some(JsonValue::Array(entries)) = root.get("issues") else {
		return Vec::new();
	};
	let mut out = Vec::new();
	for entry in entries {
		let JsonValue::Object(obj) = entry else {
			continue;
		};
		// One knip run covers the whole repository, so the entry's path is the
		// only thing attributing its findings. Taken as written — knip emits
		// project-relative paths and never a stdin placeholder.
		let file = match obj.get("file") {
			Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
			_ => None,
		};
		for (issue_type, items) in obj {
			let JsonValue::Array(items) = items else {
				continue; // "file" itself, and any future scalar field
			};
			let Some((title, has_name)) = describe(issue_type) else {
				continue;
			};
			for item in items {
				let diag = match item {
					// `duplicates` and `cycles` report groups of symbols.
					JsonValue::Array(members) => group_diag(members, title, issue_type, &file),
					_ => item_diag(item, title, has_name, issue_type, &file),
				};
				if let Some(d) = diag {
					out.push(d);
				}
			}
		}
	}
	out
}

fn item_diag(
	item: &JsonValue,
	title: &str,
	has_name: bool,
	issue_type: &str,
	file: &Option<String>,
) -> Option<RawDiagnostic> {
	let JsonValue::Object(m) = item else {
		return None;
	};
	let message = match (has_name, string_field(m, "name")) {
		(true, Some(name)) => with_namespace(format!("{title}: {name}"), m),
		// A named finding with no name is malformed output, not a finding.
		(true, None) => return None,
		(false, _) => title.to_string(),
	};
	Some(RawDiagnostic {
		message,
		row: num(m, "line"),
		col: num(m, "col"),
		severity: Some(severity::ERROR),
		code: Some(issue_type.to_string()),
		file: file.clone(),
		..RawDiagnostic::default()
	})
}

/// One diagnostic per group, placed at its first member and listing them all.
/// knip joins a cycle with arrows and everything else with commas; keeping that
/// is what preserves the direction of a cycle.
fn group_diag(members: &[JsonValue], title: &str, issue_type: &str, file: &Option<String>) -> Option<RawDiagnostic> {
	let separator = if issue_type == "cycles" { " → " } else { ", " };
	let mut names = Vec::new();
	let mut head: Option<&HashMap<String, JsonValue>> = None;
	for member in members {
		let JsonValue::Object(m) = member else {
			continue;
		};
		if let Some(name) = string_field(m, "name") {
			names.push(name);
			if head.is_none() {
				head = Some(m);
			}
		}
	}
	let head = head?;
	Some(RawDiagnostic {
		message: with_namespace(format!("{title}: {}", names.join(separator)), head),
		row: num(head, "line"),
		col: num(head, "col"),
		severity: Some(severity::ERROR),
		code: Some(issue_type.to_string()),
		file: file.clone(),
		..RawDiagnostic::default()
	})
}

/// knip suffixes a finding with its parent symbol — the enum or namespace a
/// member belongs to, which the member's own name does not carry.
fn with_namespace(message: String, m: &HashMap<String, JsonValue>) -> String {
	match string_field(m, "namespace") {
		Some(namespace) => format!("{message} ({namespace})"),
		None => message,
	}
}

fn string_field(m: &HashMap<String, JsonValue>, key: &str) -> Option<String> {
	match m.get(key) {
		Some(JsonValue::String(s)) if !s.is_empty() => Some(s.clone()),
		_ => None,
	}
}

fn num(m: &HashMap<String, JsonValue>, key: &str) -> Option<u32> {
	match m.get(key) {
		Some(JsonValue::Number(n)) => crate::numconv::json_u32(*n),
		_ => None,
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	// Shaped like real `knip --reporter json` output: findings grouped per file,
	// the empty arrays knip emits for every reported type, and a group type.
	const SAMPLE: &[u8] = br#"{"issues":[
        {"file":"src/dead.ts","files":[{"name":"src/dead.ts"}],"exports":[],"unlisted":[]},
        {"file":"src/a.ts","exports":[{"name":"helper","line":4,"col":14,"pos":120}],
         "types":[{"name":"Props","line":9,"col":13}]},
        {"file":"package.json","devDependencies":[{"name":"left-pad","line":38,"col":6}],
         "binaries":[{"name":"eslint"}]},
        {"file":"src/ids.ts","duplicates":[[{"name":"ID","line":11,"col":14},{"name":"DEFAULT_ID","line":21,"col":14}]]}
    ]}"#;

	fn find<'a>(out: &'a [RawDiagnostic], message: &str) -> &'a RawDiagnostic {
		out
			.iter()
			.find(|d| d.message == message)
			.unwrap_or_else(|| panic!("no diagnostic {message:?} in {out:#?}"))
	}

	fn parse_one(json: &str) -> RawDiagnostic {
		let out = parse(json.as_bytes(), b"", 1);
		assert_eq!(out.len(), 1, "expected exactly one diagnostic from {json}");
		out.into_iter().next().unwrap()
	}

	#[test]
	fn attributes_every_finding_to_its_file() {
		let out = parse(SAMPLE, b"", 1);
		assert_eq!(out.len(), 6);
		assert!(out.iter().all(|d| d.file.is_some()));
		// Every finding is an error. Left unset, the core's fallback renders a
		// failing run — knip exits non-zero on any finding — as a page of
		// warnings, which is what this looked like end to end before the fix.
		assert!(out.iter().all(|d| d.severity == Some(severity::ERROR)));
	}

	#[test]
	fn locates_a_finding_where_knip_does() {
		let out = parse(SAMPLE, b"", 1);

		let unused_file = find(&out, "Unused file");
		assert_eq!(unused_file.file.as_deref(), Some("src/dead.ts"));
		// A whole-file finding carries no position, and must not invent one.
		assert_eq!((unused_file.row, unused_file.col), (None, None));
		assert_eq!(unused_file.code.as_deref(), Some("files"));

		let export = find(&out, "Unused export: helper");
		assert_eq!((export.row, export.col), (Some(4), Some(14)));
		assert_eq!(export.file.as_deref(), Some("src/a.ts"));

		let dependency = find(&out, "Unused devDependency: left-pad");
		assert_eq!(dependency.file.as_deref(), Some("package.json"));
		assert_eq!((dependency.row, dependency.col), (Some(38), Some(6)));
	}

	#[test]
	fn uses_knips_own_wording_for_every_issue_type() {
		// Straight from knip's ISSUE_TYPE_TITLE, singularized as its reporters do.
		// Two of these do NOT mean "unused", and paraphrasing them would invert
		// what the check found.
		let cases = [
			("binaries", "Unlisted binary: x"),
			("catalog", "Unused catalog entry: x"),
			("catalogReferences", "Unresolved catalog reference: x"),
			("dependencies", "Unused dependency: x"),
			("devDependencies", "Unused devDependency: x"),
			("enumMembers", "Unused exported enum member: x"),
			("exports", "Unused export: x"),
			("namespaceMembers", "Unused exported namespace member: x"),
			("nsExports", "Exports in used namespace: x"),
			("nsTypes", "Exported types in used namespace: x"),
			("optionalPeerDependencies", "Referenced optional peerDependency: x"),
			("types", "Unused exported type: x"),
			("unlisted", "Unlisted dependency: x"),
			("unresolved", "Unresolved import: x"),
		];
		for (issue_type, expected) in cases {
			let json = format!(r#"{{"issues":[{{"file":"f.ts","{issue_type}":[{{"name":"x"}}]}}]}}"#);
			let diag = parse_one(&json);
			assert_eq!(diag.message, expected, "wording for {issue_type}");
			assert_eq!(diag.code.as_deref(), Some(issue_type));
		}
	}

	#[test]
	fn folds_a_duplicate_group_into_one_diagnostic_at_its_first_member() {
		let out = parse(SAMPLE, b"", 1);
		let duplicate = find(&out, "Duplicate export: ID, DEFAULT_ID");

		assert_eq!((duplicate.row, duplicate.col), (Some(11), Some(14)));
		assert_eq!(duplicate.code.as_deref(), Some("duplicates"));
	}

	#[test]
	fn joins_a_cycle_with_arrows_as_knip_does() {
		let diag = parse_one(r#"{"issues":[{"file":"src/a.ts","cycles":[[{"name":"src/a.ts"},{"name":"src/b.ts"}]]}]}"#);

		assert_eq!(diag.message, "Circular dependency: src/a.ts → src/b.ts");
		assert_eq!(diag.code.as_deref(), Some("cycles"));
	}

	#[test]
	fn names_the_parent_symbol_a_member_belongs_to() {
		// knip's own description appends the parent symbol, because a member name
		// alone does not say which enum or namespace it came from.
		let diag = parse_one(
			r#"{"issues":[{"file":"src/e.ts","enumMembers":[{"name":"Red","namespace":"Color","line":3,"col":3}]}]}"#,
		);

		assert_eq!(diag.message, "Unused exported enum member: Red (Color)");

		// The `ns*` types carry a parent symbol too — the namespace the module was
		// imported under. Verbatim shape of a real report: knip emits these only
		// when a module is imported as a namespace that is then used whole, with
		// no member access anywhere, so nothing else in this suite produces one.
		let ns = parse_one(
			r#"{"issues":[{"file":"src/shapes.ts","nsTypes":[{"namespace":"shapes","name":"Circle","line":1,"col":13,"pos":12}]}]}"#,
		);

		assert_eq!(ns.message, "Exported types in used namespace: Circle (shapes)");
		assert_eq!((ns.row, ns.col), (Some(1), Some(13)));
		assert_eq!(ns.code.as_deref(), Some("nsTypes"));
	}

	#[test]
	fn reports_nothing_for_a_clean_run() {
		assert!(parse(br#"{"issues":[]}"#, b"", 0).is_empty());
	}

	#[test]
	fn ignores_annotations_and_types_this_build_does_not_know() {
		let diag = parse_one(
			r#"{"issues":[{"file":"src/a.ts","owners":[{"name":"@team"}],"somethingNew":[{"name":"x"}],
                "files":[{"name":"src/a.ts"}]}]}"#,
		);

		assert_eq!(diag.message, "Unused file");
	}

	#[test]
	fn survives_noise_around_the_report() {
		for (label, noisy) in [
			("before", format!("plugin chatter\n{}", String::from_utf8_lossy(SAMPLE))),
			(
				"after",
				format!("{}\ntrailing chatter", String::from_utf8_lossy(SAMPLE)),
			),
		] {
			assert_eq!(parse(noisy.as_bytes(), b"", 1).len(), 6, "noise {label} the report");
		}
	}

	#[test]
	fn yields_nothing_rather_than_guessing_on_malformed_input() {
		for input in [
			&b""[..],
			b"not json at all",
			br#"{"issues":"#,                                    // truncated mid-report
			br#"{"issues":{}}"#,                                 // issues is not an array
			br#"[{"file":"a.ts"}]"#,                             // top level is not the report object
			br#"{"issues":["a.ts",42,null]}"#,                   // entries are not objects
			br#"{"issues":[{"file":"a.ts","exports":"nope"}]}"#, // findings are not an array
		] {
			assert!(
				parse(input, b"", 1).is_empty(),
				"input {:?}",
				String::from_utf8_lossy(input)
			);
		}
	}

	#[test]
	fn skips_a_finding_whose_name_is_missing_or_not_a_string() {
		for items in [r#"[{}]"#, r#"[{"name":null}]"#, r#"[{"name":42}]"#, r#"[{"name":""}]"#] {
			let json = format!(r#"{{"issues":[{{"file":"a.ts","exports":{items}}}]}}"#);
			assert!(parse(json.as_bytes(), b"", 1).is_empty(), "items {items}");
		}
		// A group with no usable member is dropped whole rather than reported empty.
		assert!(parse(br#"{"issues":[{"file":"a.ts","duplicates":[[],[{}]]}]}"#, b"", 1).is_empty());
	}

	#[test]
	fn drops_a_position_that_is_not_a_usable_line_number() {
		// A position this parser cannot trust is absent, never a plausible-looking
		// substitute. (`numconv::json_u32` also accepts a fractional number and
		// truncates it, unlike its `json_int` sibling and its own doc comment — so
		// `0.5` is deliberately not in this list.)
		for position in [r#""7""#, "-1", "null", "99999999999", "true", r#"{}"#] {
			let json = format!(r#"{{"issues":[{{"file":"a.ts","exports":[{{"name":"x","line":{position}}}]}}]}}"#);
			let diag = parse_one(&json);
			assert_eq!(diag.row, None, "line {position}");
			assert_eq!(diag.message, "Unused export: x");
		}
	}

	#[test]
	fn keeps_a_finding_whose_entry_names_no_file() {
		// Attribution is lost, but the finding itself is still real.
		for entry in [
			r#"{"exports":[{"name":"x"}]}"#,
			r#"{"file":"","exports":[{"name":"x"}]}"#,
		] {
			let diag = parse_one(&format!(r#"{{"issues":[{entry}]}}"#));
			assert_eq!(diag.file, None);
			assert_eq!(diag.message, "Unused export: x");
		}
	}

	#[test]
	fn carries_non_ascii_paths_and_symbols_through_intact() {
		let diag = parse_one(r#"{"issues":[{"file":"src/файл.ts","exports":[{"name":"переменная","line":2,"col":1}]}]}"#);

		assert_eq!(diag.file.as_deref(), Some("src/файл.ts"));
		assert_eq!(diag.message, "Unused export: переменная");
	}

	#[test]
	fn reports_every_finding_of_a_file_that_has_several() {
		let out = parse(
			br#"{"issues":[{"file":"src/a.ts","exports":[{"name":"a"},{"name":"b"}],
                "types":[{"name":"T"}],"unlisted":[{"name":"pkg"}]}]}"#,
			b"",
			1,
		);

		assert_eq!(out.len(), 4);
		assert!(out.iter().all(|d| d.file.as_deref() == Some("src/a.ts")));
	}
}
