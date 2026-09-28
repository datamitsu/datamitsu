//! What every parser promises, checked for every parser at once.
//!
//! - **Levels.** A parser sets a severity only from a token the tool printed,
//!   and its descriptor lists those tokens (`severities`). A parser whose
//!   vocabulary is empty never returns a level; one whose vocabulary is
//!   non-empty returns only levels that vocabulary maps to. Checked over every
//!   sample of every tool and every recorded fixture, and in the source: outside
//!   its tests, a parser names a level constant only inside a `Level(..)`
//!   vocabulary entry.
//! - **Rule identity.** `source` is the tool's name — the same string for every
//!   finding of a tool — never the rule, which goes into `code`.
//! - **Positions.** Rows and columns are 1-based, and an end column is
//!   exclusive. No sample can tell a 0-based column from a 1-based one, so the
//!   module-wide check is only that an end never precedes its start; each
//!   parser's own tests pin the coordinates its tool's convention gives, and
//!   [`POSITIONS`] records that convention for every parser, so a new parser
//!   cannot skip the audit.
//! - **Descriptor.** Every tool declares a column unit or sits on
//!   [`UNKNOWN_COLUMN_UNITS`], and every category and kind is a known one.
//!
//! The format parsers and the sniffer are held to the same checks. None of them
//! has a column unit: a format carries whatever unit the tool that printed it
//! counts in, so they are all on the unknown list.

use std::collections::{BTreeMap, BTreeSet};

use crate::capabilities::{described, ToolCapability};
use crate::diagnostic::RawDiagnostic;

/// One output of a tool, as the core would hand it to the parser.
#[derive(Clone, Copy)]
pub(crate) struct Sample {
	pub(crate) stdout: &'static [u8],
	pub(crate) stderr: &'static [u8],
	pub(crate) exit: i32,
}

/// The column units a descriptor may declare, in LSP's vocabulary.
const COLUMN_UNITS: &[&str] = &["utf-8", "utf-16", "utf-32"];

/// The categories a descriptor may declare besides none.
const CATEGORIES: &[&str] = &["security"];

/// The parsers whose tool's column unit has not been measured. A tool leaves
/// this list when a measurement on a line holding multi-byte characters sets its
/// descriptor's `column_unit`; a new parser either declares one or joins the
/// list. Among the tools the reference configuration wires, checkmake and
/// dotenv-linter print no column, and dclint and hadolint print column 1 for
/// every finding, so no measurement can tell their unit.
#[cfg(feature = "tools")]
const UNKNOWN_COLUMN_UNITS: &[&str] = &[
	"alex",
	"ansiblelint",
	"bean_check",
	"bslint",
	"buf",
	"buildifier",
	"cfn_lint",
	"checkmake",
	"checkstyle",
	"clazy",
	"clj_kondo",
	"cmake_lint",
	"codespell",
	"commitlint",
	"cppcheck",
	"credo",
	"cue_fmt",
	"dclint",
	"deadnix",
	"djlint",
	"dotenv_linter",
	"droast",
	"editorconfig_checker",
	"erb_lint",
	"fish",
	"gccdiag",
	"gdlint",
	"gitleaks",
	"gitlint",
	"glslc",
	"hadolint",
	"haml_lint",
	"knip",
	"ktlint",
	"kube_linter",
	"ltrs",
	"markdownlint",
	"markdownlint_cli2",
	"markuplint",
	"mdl",
	"mlint",
	"mypy",
	"npm_groovy_lint",
	"opacheck",
	"opentofu_validate",
	"perlimports",
	"phpcs",
	"phpmd",
	"phpstan",
	"pmd",
	"proselint",
	"puppet_lint",
	"pydoclint",
	"pylint",
	"qmllint",
	"reek",
	"regal",
	"revive",
	"rpmspec",
	"rstcheck",
	"rubocop",
	"saltlint",
	"selene",
	"semgrep",
	"solhint",
	"spectral",
	"sqlfluff",
	"sqruff",
	"staticcheck",
	"statix",
	"stylint",
	"swiftlint",
	"teal",
	"terraform_validate",
	"terragrunt_validate",
	"textidote",
	"textlint",
	"tfsec",
	"tidy",
	"trivy",
	"twigcs",
	"vacuum",
	"verilator",
	"vint",
	"write_good",
	"zsh",
];

/// The position audit: for every parser, what its tool prints and what the
/// parser does about it. "1-based" means the tool counts from 1 and the parser
/// passes the value through; "exclusive" names the column after the span.
/// "Unverified" marks a convention taken from the tool's documentation or the
/// ported builtin without a run of the tool.
#[cfg(feature = "tools")]
const POSITIONS: &[(&str, &str)] = &[
	("actionlint", "1-based line and column; end_column is the last column of the span (inclusive), +1; filepath names the file"),
	("alex", "1-based line and column with an end line and an exclusive end column"),
	("ansiblelint", "1-based line, optional 1-based column, no end"),
	("bean_check", "1-based line only"),
	("bslint", "1-based line and column (the tool adds 1 to its 0-based range), no end"),
	("buf", "1-based line and column, no end"),
	("buildifier", "1-based line and column; the end is the position after the span (exclusive)"),
	("cfn_lint", "1-based line and column with an end line and an exclusive end column"),
	("checkmake", "1-based line only"),
	("checkstyle", "SARIF region: 1-based startLine and optional startColumn, no end"),
	("clazy", "1-based line and column (clang), no end"),
	("clj_kondo", "1-based line and column, no end"),
	("cmake_lint", "1-based line, 0-based column: +1 to the column; no end"),
	("codespell", "1-based line only"),
	("commitlint", "no position printed; body-leading-blank is on line 2 by definition, every other rule has no row"),
	("cppcheck", "1-based line and column, no end; 0:0 for a finding without a location"),
	("credo", "1-based line and column; column_end is exclusive; no end line"),
	("cspell", "1-based line and column, no end"),
	("cue_fmt", "1-based line and column, no end"),
	("dclint", "1-based line and column; endLine/endColumn passed through (end convention unverified)"),
	("deadnix", "1-based line and column, exclusive endColumn (unverified)"),
	("djlint", "1-based line, 0-based column: +1 to the column; no end (unverified)"),
	("dotenv_linter", "1-based line only"),
	("droast", "1-based line and column, 0 for none; exclusive end line and column"),
	("editorconfig_checker", "1-based line only"),
	("erb_lint", "1-based line; 0-based start_column and exclusive last_column: +1 to both (unverified)"),
	("eslint", "1-based line and column; endColumn exclusive"),
	("fish", "1-based line only"),
	("gccdiag", "1-based line and column, no end"),
	("gdlint", "1-based line only (unverified)"),
	("gitleaks", "1-based absolute line; columns count within a scanned fragment: kept on line 1 (inclusive end +1), dropped after it"),
	("gitlint", "1-based line only (unverified)"),
	("glslc", "1-based line only"),
	("golangci_lint", "1-based line and byte column, no end"),
	("hadolint", "1-based line and column (column 1 in practice), no end"),
	("haml_lint", "1-based line only (unverified)"),
	("harper_cli", "1-based line and column, no end"),
	("knip", "1-based line and column (the tool adds 1 to TypeScript's 0-based position), no end (unverified)"),
	("ktlint", "1-based line and column, no end"),
	("kube_linter", "no position printed"),
	("ltrs", "1-based line (with --more-context); 0-based offset: col = offset + 1, exclusive end = offset + length + 1"),
	("markdownlint", "1-based line and column, no end"),
	("markdownlint_cli2", "1-based line and column, no end"),
	("markuplint", "1-based line and column, no end"),
	("mdl", "1-based line only"),
	("mlint", "1-based line and column; the (C a-b) range ends on its last column (inclusive), +1"),
	("mypy", "1-based line and column, no end"),
	("npm_groovy_lint", "1-based range lines; 0-based range characters with an exclusive end: +1 to both columns"),
	("opacheck", "1-based row and column, no end"),
	("opentofu_validate", "1-based line and column with an exclusive end"),
	("perlimports", "1-based line only"),
	("phpcs", "1-based line and column, no end"),
	("phpmd", "1-based beginLine and endLine, no column"),
	("phpstan", "1-based line only"),
	("pmd", "1-based begin/end line and column; PMD 7's end column is exclusive"),
	("proselint", "1-based line and column, no end (the span may cross a line break)"),
	("protolint", "1-based line and column, no end"),
	("puppet_lint", "1-based line and column, no end"),
	("pydoclint", "1-based line only"),
	("pylint", "1-based line and endLine; 0-based column and endColumn: +1 to both, exclusive end"),
	("qmllint", "1-based line and column, no end"),
	("reek", "1-based lines, one finding per line, no column"),
	("regal", "1-based row and column; location.end is exclusive when printed, no end otherwise"),
	("revive", "go/token 1-based line and column; End is exclusive"),
	("rpmspec", "1-based line only"),
	("rstcheck", "1-based line only"),
	("rubocop", "1-based start line and column; last_column names the last character: +1 for an exclusive end"),
	("saltlint", "1-based line only"),
	("selene", "0-based line and column in json2: +1 to all four, exclusive end"),
	("semgrep", "1-based line and column with an exclusive end"),
	("solhint", "1-based line and column (the reporter adds 1), no end"),
	("spectral", "0-based line and character: +1 to start and end, exclusive end"),
	("sqlfluff", "1-based line and column with an exclusive end"),
	("sqruff", "1-based line and column, no end"),
	("staticcheck", "go/token 1-based line and column; end exclusive; line 0 means no position"),
	("statix", "1-based row and column, no end"),
	("stylint", "1-based row and column, no end (unverified)"),
	("swiftlint", "1-based line and character, no end; a null character gives no column"),
	("teal", "1-based line and column, no end"),
	("terraform_validate", "1-based line and column with an exclusive end"),
	("terragrunt_validate", "1-based line and column with an exclusive end"),
	("textidote", "L<row>C<col> 1-based; the end names the last character (inclusive), +1"),
	("textlint", "1-based line and column, no end"),
	("tfsec", "1-based start_line and end_line (the last line), no column"),
	("tidy", "1-based line and column, no end"),
	("trivy", "1-based StartLine and EndLine (the last line), no column; 0 means none"),
	("tsc", "1-based line and column, no end"),
	("twigcs", "1-based line and column, no end (unverified)"),
	("vacuum", "1-based line and character in the report, exclusive end"),
	("vale", "1-based line; Span[0] the start column, Span[1] the last column (inclusive), +1"),
	("verilator", "1-based line and column, no end"),
	("vint", "1-based line and column, no end (unverified)"),
	("write_good", "1-based line, 0-based column: +1 to the column; no end (unverified)"),
	("yamllint", "1-based line and column, no end"),
	("zsh", "1-based line only"),
];

/// The format parsers' column units, all unknown: a format counts in the unit
/// of whichever tool printed it.
const FORMAT_UNKNOWN_COLUMN_UNITS: &[&str] = &[
	"azure-logissue",
	"checkstyle-xml",
	"codeclimate",
	"eslint-json",
	"fallback",
	"gcc",
	"github-annotations",
	"json",
	"junit-xml",
	"msvc",
	"sarif",
];

/// The position audit of the format parsers.
const FORMAT_POSITIONS: &[(&str, &str)] = &[
	("azure-logissue", "1-based linenumber and columnnumber, no end"),
	("checkstyle-xml", "1-based line and column, no end"),
	(
		"codeclimate",
		"1-based lines.begin/end, or positions begin/end line and column, passed through",
	),
	("eslint-json", "1-based line and column; endColumn exclusive"),
	("fallback", "the positions of the format it picks"),
	("gcc", "1-based line and column, no end"),
	(
		"github-annotations",
		"1-based line and col; endLine and endColumn passed through",
	),
	("json", "line, column, endLine and endColumn passed through as printed"),
	(
		"junit-xml",
		"the test case's 1-based line, or the line and column of a path:line:col classname; no end",
	),
	("msvc", "1-based line and column, no end"),
	(
		"sarif",
		"1-based startLine and startColumn; endColumn exclusive by the specification",
	),
];

#[cfg(not(feature = "tools"))]
const UNKNOWN_COLUMN_UNITS: &[&str] = &[];

#[cfg(not(feature = "tools"))]
const POSITIONS: &[(&str, &str)] = &[];

/// Every parser whose column unit is unknown, in this build.
fn unknown_column_units() -> Vec<&'static str> {
	[FORMAT_UNKNOWN_COLUMN_UNITS, UNKNOWN_COLUMN_UNITS].concat()
}

/// The position audit of every parser in this build.
fn positions() -> Vec<(&'static str, &'static str)> {
	[FORMAT_POSITIONS, POSITIONS].concat()
}

fn real_tools() -> impl Iterator<Item = &'static ToolCapability> {
	described().into_iter().filter(|t| t.name != "echo")
}

/// Every sample of `tool`: its module's own and its recorded fixtures.
fn samples_of(tool: &str) -> Vec<Sample> {
	[own_samples(tool).unwrap_or(&[]).to_vec(), recorded(tool)].concat()
}

/// The recorded runs of `tool`: a tool's own, or those of tools printing a
/// format.
fn recorded(tool: &str) -> Vec<Sample> {
	recordings()
		.into_iter()
		.filter(|(key, _)| *key == tool)
		.map(|(_, s)| s)
		.collect()
}

#[cfg(feature = "tools")]
fn recordings() -> Vec<(&'static str, Sample)> {
	[crate::tools::fixtures::recorded(), crate::format::fixtures::recorded()].concat()
}

#[cfg(not(feature = "tools"))]
fn recordings() -> Vec<(&'static str, Sample)> {
	crate::format::fixtures::recorded()
}

fn own_samples(tool: &str) -> Option<&'static [Sample]> {
	#[cfg(feature = "tools")]
	if let Some(s) = crate::tools::samples(tool) {
		return Some(s);
	}
	crate::format::samples(tool)
}

fn parse(tool: &str, s: &Sample) -> Vec<RawDiagnostic> {
	crate::answer(tool, s.stdout, s.stderr, s.exit).diagnostics
}

/// The source file of a parser.
fn source_of(t: &ToolCapability) -> String {
	let src = concat!(env!("CARGO_MANIFEST_DIR"), "/src");
	match (t.kind, t.name) {
		("format", "fallback") => format!("{src}/fallback.rs"),
		("format", name) => format!("{src}/format/{}.rs", name.replace('-', "_")),
		(_, name) => format!("{src}/tools/{name}.rs"),
	}
}

/// Collects every violation of one check, so a failing run names every parser
/// that breaks it rather than the first.
#[derive(Default)]
struct Violations(Vec<String>);

impl Violations {
	fn check(&mut self, ok: bool, message: impl FnOnce() -> String) {
		if !ok {
			self.0.push(message());
		}
	}

	fn assert_none(self, what: &str) {
		assert!(self.0.is_empty(), "{what}:\n  {}", self.0.join("\n  "));
	}
}

#[test]
fn every_tool_has_samples() {
	let mut v = Violations::default();
	for t in real_tools() {
		v.check(own_samples(t.name).is_some_and(|s| !s.is_empty()), || {
			format!("{}: no SAMPLES", t.name)
		});
	}
	v.assert_none("every parser gives the contract something to check");
}

#[test]
fn levels_come_only_from_the_vocabulary() {
	let mut v = Violations::default();
	for t in real_tools() {
		let allowed: BTreeSet<u8> = t.severities.iter().map(|l| l.1).collect();
		for (i, s) in samples_of(t.name).iter().enumerate() {
			for d in parse(t.name, s) {
				if let Some(level) = d.severity {
					v.check(allowed.contains(&level), || {
						format!(
							"{} sample {i}: level {level} is not in its vocabulary {:?}: {d:?}",
							t.name,
							t.severities.iter().map(|l| l.0).collect::<Vec<_>>()
						)
					});
				}
			}
		}
	}
	v.assert_none("a level must come from a token the tool printed");
}

#[test]
fn a_parser_names_a_level_only_in_its_vocabulary() {
	let mut v = Violations::default();
	for t in real_tools() {
		let path = source_of(t);
		let source = std::fs::read_to_string(&path).expect("the parser's module");
		let code = source.split("#[cfg(test)]").next().unwrap_or("");
		for (n, line) in code.lines().enumerate() {
			let names_a_level = ["ERROR", "WARNING", "INFO", "HINT"]
				.iter()
				.any(|c| line.contains(&format!("severity::{c}")));
			v.check(!names_a_level || line.contains("Level("), || {
				format!("{}.rs:{}: {}", t.name, n + 1, line.trim())
			});
		}
	}
	v.assert_none("outside its tests a parser names a level only in a Level(..) vocabulary entry");
}

#[test]
fn every_vocabulary_is_well_formed() {
	let mut v = Violations::default();
	let mut names = BTreeSet::new();
	for t in described() {
		v.check(names.insert(t.name), || format!("{}: described twice", t.name));
		let mut tokens = BTreeSet::new();
		for l in t.severities {
			v.check(!l.0.is_empty(), || format!("{}: an empty level token", t.name));
			v.check(tokens.insert(l.0), || {
				format!("{}: token {:?} listed twice", t.name, l.0)
			});
			v.check((1..=4).contains(&l.1), || {
				format!("{}: token {:?} maps to {}", t.name, l.0, l.1)
			});
		}
	}
	v.assert_none("vocabularies");
}

#[test]
fn the_source_names_the_tool_never_the_rule() {
	let mut v = Violations::default();
	for t in real_tools() {
		let mut sources = BTreeSet::new();
		for s in &samples_of(t.name) {
			for d in parse(t.name, s) {
				if let Some(source) = d.source {
					sources.insert(source);
				}
			}
		}
		v.check(sources.len() <= 1, || {
			format!("{}: more than one source {sources:?}", t.name)
		});
	}
	v.assert_none("a source is the tool's name, one per tool");
}

#[test]
fn an_end_never_precedes_its_start() {
	let mut v = Violations::default();
	for t in real_tools() {
		for (i, s) in samples_of(t.name).iter().enumerate() {
			for d in parse(t.name, s) {
				if let (Some(row), Some(end_row)) = (d.row, d.end_row) {
					v.check(end_row >= row, || {
						format!("{} sample {i}: end row before the start: {d:?}", t.name)
					});
				}
				let same_row = d.end_row.is_none() || d.end_row == d.row;
				if let (Some(col), Some(end_col), true) = (d.col, d.end_col, same_row) {
					v.check(end_col >= col, || {
						format!("{} sample {i}: end column before the start: {d:?}", t.name)
					});
				}
			}
		}
	}
	v.assert_none("positions");
}

#[test]
fn every_parser_is_in_the_position_audit() {
	let mut v = Violations::default();
	let positions = positions();
	let audited: BTreeMap<&str, &str> = positions.iter().copied().collect();
	v.check(audited.len() == positions.len(), || {
		"a parser is audited twice".to_string()
	});
	for t in real_tools() {
		v.check(audited.contains_key(t.name), || {
			format!("{}: missing from POSITIONS", t.name)
		});
	}
	for name in audited.keys() {
		v.check(described().iter().any(|t| t.name == *name), || {
			format!("{name}: audited but not described")
		});
	}
	v.assert_none("the position audit");
}

#[test]
fn every_descriptor_declares_a_known_unit_category_and_kind() {
	let mut v = Violations::default();
	let listed = unknown_column_units();
	let unknown: BTreeSet<&str> = listed.iter().copied().collect();
	v.check(unknown.len() == listed.len(), || "a parser is listed twice".to_string());
	for t in real_tools() {
		v.check(!t.column_unit.is_empty() || unknown.contains(t.name), || {
			format!("{}: no column unit and not on UNKNOWN_COLUMN_UNITS", t.name)
		});
		v.check(t.column_unit.is_empty() || !unknown.contains(t.name), || {
			format!("{}: a measured column unit, yet on UNKNOWN_COLUMN_UNITS", t.name)
		});
		v.check(
			t.column_unit.is_empty() || COLUMN_UNITS.contains(&t.column_unit),
			|| format!("{}: unknown column unit {:?}", t.name, t.column_unit),
		);
		v.check(t.category.is_empty() || CATEGORIES.contains(&t.category), || {
			format!("{}: unknown category {:?}", t.name, t.category)
		});
		let format = crate::format::DESCRIPTORS.iter().any(|f| f.name == t.name);
		let want = if format { "format" } else { "tool" };
		v.check(t.kind == want, || {
			format!("{}: kind {:?}, want {want:?}", t.name, t.kind)
		});
	}
	v.assert_none("descriptors");
}
