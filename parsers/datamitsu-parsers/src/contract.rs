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

use std::collections::{BTreeMap, BTreeSet};

use crate::capabilities::{ToolCapability, TOOLS};
use crate::diagnostic::RawDiagnostic;
use crate::tools;

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
	("commitlint", "no position printed; a row derived from the rule name, as the ported builtin does (unverified)"),
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
	("proselint", "1-based line and column; end column = column + span length (the span stays on its line)"),
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

fn real_tools() -> impl Iterator<Item = &'static ToolCapability> {
	TOOLS.iter().copied().filter(|t| t.name != "echo")
}

/// Every sample of `tool`: its module's own and its recorded fixtures.
fn samples_of(tool: &str) -> Vec<Sample> {
	let mut out: Vec<Sample> = tools::samples(tool).unwrap_or(&[]).to_vec();
	out.extend(
		tools::fixtures::recorded()
			.into_iter()
			.filter(|(key, _)| *key == tool)
			.map(|(_, s)| s),
	);
	out
}

fn parse(tool: &str, s: &Sample) -> Vec<RawDiagnostic> {
	tools::dispatch(tool, s.stdout, s.stderr, s.exit).expect("a parser the module dispatches")
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
		v.check(tools::samples(t.name).is_some_and(|s| !s.is_empty()), || {
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
	let dir = concat!(env!("CARGO_MANIFEST_DIR"), "/src/tools");
	let mut v = Violations::default();
	for t in real_tools() {
		let path = format!("{dir}/{}.rs", t.name);
		let source = std::fs::read_to_string(&path).expect("the tool's module");
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
	for &t in TOOLS {
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
	let audited: BTreeMap<&str, &str> = POSITIONS.iter().copied().collect();
	v.check(audited.len() == POSITIONS.len(), || {
		"a parser is audited twice".to_string()
	});
	for t in real_tools() {
		v.check(audited.contains_key(t.name), || {
			format!("{}: missing from POSITIONS", t.name)
		});
	}
	for name in audited.keys() {
		v.check(TOOLS.iter().any(|t| t.name == *name), || {
			format!("{name}: audited but not described")
		});
	}
	v.assert_none("the position audit");
}

#[test]
fn every_descriptor_declares_a_known_unit_category_and_kind() {
	let mut v = Violations::default();
	let unknown: BTreeSet<&str> = UNKNOWN_COLUMN_UNITS.iter().copied().collect();
	v.check(unknown.len() == UNKNOWN_COLUMN_UNITS.len(), || {
		"a parser is listed twice".to_string()
	});
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
		v.check(t.kind == "tool", || format!("{}: kind {:?}", t.name, t.kind));
	}
	v.assert_none("descriptors");
}
