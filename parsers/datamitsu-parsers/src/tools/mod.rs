//! Per-tool parsers. One module per tool, each exporting `parse` (raw tool output
//! → nullable `RawDiagnostic`s) and a `DESCRIPTOR` (its `describe` capability with
//! the recommended invocation). Adding a tool is: a new module here, one arm in
//! [`dispatch`], and one entry in [`DESCRIPTORS`].
//!
//! Parsers are **hand-written** (no `regex` dependency) and ported faithfully from
//! the upstream none-ls builtin / efm errorformat. The JSON-output class shares the
//! `json_diag` helper (its one external crate, `tinyjson`).

pub mod actionlint;
pub mod alex;
pub mod ansiblelint;
pub mod bean_check;
pub mod bslint;
pub mod buf;
pub mod buildifier;
pub mod cfn_lint;
pub mod checkmake;
pub mod checkstyle;
pub mod clazy;
pub mod clj_kondo;
pub mod cmake_lint;
pub mod codespell;
pub mod commitlint;
pub mod cppcheck;
pub mod credo;
pub mod cspell;
pub mod cue_fmt;
pub mod dclint;
pub mod deadnix;
pub mod djlint;
pub mod dotenv_linter;
pub mod droast;
pub mod editorconfig_checker;
pub mod erb_lint;
pub mod eslint;
pub mod fish;
pub mod gccdiag;
pub mod gdlint;
pub mod gitleaks;
pub mod gitlint;
pub mod glslc;
pub mod golangci_lint;
pub mod hadolint;
pub mod haml_lint;
pub mod harper_cli;
pub mod knip;
pub mod ktlint;
pub mod kube_linter;
pub mod ltrs;
pub mod markdownlint;
pub mod markdownlint_cli2;
pub mod markuplint;
pub mod mdl;
pub mod mlint;
pub mod mypy;
pub mod npm_groovy_lint;
pub mod opacheck;
pub mod opentofu_validate;
pub mod perlimports;
pub mod phpcs;
pub mod phpmd;
pub mod phpstan;
pub mod pmd;
pub mod proselint;
pub mod protolint;
pub mod puppet_lint;
pub mod pydoclint;
pub mod pylint;
pub mod qmllint;
pub mod reek;
pub mod regal;
pub mod revive;
pub mod rpmspec;
pub mod rstcheck;
pub mod rubocop;
pub mod saltlint;
pub mod selene;
pub mod semgrep;
pub mod solhint;
pub mod spectral;
pub mod sqlfluff;
pub mod sqruff;
pub mod staticcheck;
pub mod statix;
pub mod stylint;
pub mod swiftlint;
pub mod teal;
pub mod terraform_validate;
pub mod terragrunt_validate;
pub mod textidote;
pub mod textlint;
pub mod tfsec;
pub mod tidy;
pub mod trivy;
pub mod tsc;
pub mod twigcs;
pub mod vacuum;
pub mod vale;
pub mod verilator;
pub mod vint;
pub mod write_good;
pub mod yamllint;
pub mod zsh;

#[cfg(test)]
pub(crate) mod fixtures;

/// A tool module's contract samples (`SAMPLES`), by dispatch name, for the
/// checks every parser must pass (`crate::contract`).
#[cfg(test)]
pub(crate) fn samples(tool: &str) -> Option<&'static [crate::contract::Sample]> {
	macro_rules! by_name {
		($($m:ident),* $(,)?) => {
			match tool {
				$(stringify!($m) => Some($m::SAMPLES),)*
				_ => None,
			}
		};
	}
	by_name!(
		actionlint,
		alex,
		ansiblelint,
		bean_check,
		bslint,
		buf,
		buildifier,
		cfn_lint,
		checkmake,
		checkstyle,
		clazy,
		clj_kondo,
		cmake_lint,
		codespell,
		commitlint,
		cppcheck,
		credo,
		cspell,
		cue_fmt,
		dclint,
		deadnix,
		djlint,
		dotenv_linter,
		droast,
		editorconfig_checker,
		erb_lint,
		eslint,
		fish,
		gccdiag,
		gdlint,
		gitleaks,
		gitlint,
		glslc,
		golangci_lint,
		hadolint,
		haml_lint,
		harper_cli,
		knip,
		ktlint,
		kube_linter,
		ltrs,
		markdownlint,
		markdownlint_cli2,
		markuplint,
		mdl,
		mlint,
		mypy,
		npm_groovy_lint,
		opacheck,
		opentofu_validate,
		perlimports,
		phpcs,
		phpmd,
		phpstan,
		pmd,
		proselint,
		protolint,
		puppet_lint,
		pydoclint,
		pylint,
		qmllint,
		reek,
		regal,
		revive,
		rpmspec,
		rstcheck,
		rubocop,
		saltlint,
		selene,
		semgrep,
		solhint,
		spectral,
		sqlfluff,
		sqruff,
		staticcheck,
		statix,
		stylint,
		swiftlint,
		teal,
		terraform_validate,
		terragrunt_validate,
		textidote,
		textlint,
		tfsec,
		tidy,
		trivy,
		tsc,
		twigcs,
		vacuum,
		vale,
		verilator,
		vint,
		write_good,
		yamllint,
		zsh,
	)
}

use crate::capabilities::ToolCapability;
use crate::diagnostic::RawDiagnostic;
use crate::response::Response;

/// The `echo` pipe-test parser's descriptor; `echo` itself lives in the crate
/// root.
const ECHO: ToolCapability = ToolCapability {
	name: "echo",
	description: "Pipe-test parser: echoes stdout into a single diagnostic message and the \
        exit code into `code`. Proves the declare\u{2192}build\u{2192}sign\u{2192}deliver\u{2192}load\u{2192}invoke \
        pipe end to end; not a real tool.",
	url: "",
	operations: &[],
	severities: &[],
	column_unit: "",
	category: "",
	kind: "tool",
};

/// The capability table: the pipe-test `echo` plus one entry per real tool. A new
/// tool adds its module's `DESCRIPTOR` here (and a [`dispatch`] arm). It lives
/// here rather than in `capabilities`, which the embedded fallback module is
/// built from: adding a tool must not change a source of that build.
pub(crate) const DESCRIPTORS: &[&ToolCapability] = &[
	&ECHO,
	&actionlint::DESCRIPTOR,
	&alex::DESCRIPTOR,
	&ansiblelint::DESCRIPTOR,
	&bean_check::DESCRIPTOR,
	&bslint::DESCRIPTOR,
	&buf::DESCRIPTOR,
	&buildifier::DESCRIPTOR,
	&cfn_lint::DESCRIPTOR,
	&checkmake::DESCRIPTOR,
	&checkstyle::DESCRIPTOR,
	&clazy::DESCRIPTOR,
	&clj_kondo::DESCRIPTOR,
	&cmake_lint::DESCRIPTOR,
	&codespell::DESCRIPTOR,
	&commitlint::DESCRIPTOR,
	&cppcheck::DESCRIPTOR,
	&credo::DESCRIPTOR,
	&cspell::DESCRIPTOR,
	&cue_fmt::DESCRIPTOR,
	&dclint::DESCRIPTOR,
	&deadnix::DESCRIPTOR,
	&djlint::DESCRIPTOR,
	&dotenv_linter::DESCRIPTOR,
	&droast::DESCRIPTOR,
	&editorconfig_checker::DESCRIPTOR,
	&erb_lint::DESCRIPTOR,
	&eslint::DESCRIPTOR,
	&fish::DESCRIPTOR,
	&gccdiag::DESCRIPTOR,
	&gdlint::DESCRIPTOR,
	&gitleaks::DESCRIPTOR,
	&gitlint::DESCRIPTOR,
	&glslc::DESCRIPTOR,
	&golangci_lint::DESCRIPTOR,
	&hadolint::DESCRIPTOR,
	&haml_lint::DESCRIPTOR,
	&harper_cli::DESCRIPTOR,
	&knip::DESCRIPTOR,
	&ktlint::DESCRIPTOR,
	&kube_linter::DESCRIPTOR,
	&ltrs::DESCRIPTOR,
	&markdownlint::DESCRIPTOR,
	&markdownlint_cli2::DESCRIPTOR,
	&markuplint::DESCRIPTOR,
	&mdl::DESCRIPTOR,
	&mlint::DESCRIPTOR,
	&mypy::DESCRIPTOR,
	&npm_groovy_lint::DESCRIPTOR,
	&opacheck::DESCRIPTOR,
	&opentofu_validate::DESCRIPTOR,
	&perlimports::DESCRIPTOR,
	&phpcs::DESCRIPTOR,
	&phpmd::DESCRIPTOR,
	&phpstan::DESCRIPTOR,
	&pmd::DESCRIPTOR,
	&proselint::DESCRIPTOR,
	&protolint::DESCRIPTOR,
	&puppet_lint::DESCRIPTOR,
	&pydoclint::DESCRIPTOR,
	&pylint::DESCRIPTOR,
	&qmllint::DESCRIPTOR,
	&reek::DESCRIPTOR,
	&regal::DESCRIPTOR,
	&revive::DESCRIPTOR,
	&rpmspec::DESCRIPTOR,
	&rstcheck::DESCRIPTOR,
	&rubocop::DESCRIPTOR,
	&saltlint::DESCRIPTOR,
	&selene::DESCRIPTOR,
	&semgrep::DESCRIPTOR,
	&solhint::DESCRIPTOR,
	&spectral::DESCRIPTOR,
	&sqlfluff::DESCRIPTOR,
	&sqruff::DESCRIPTOR,
	&staticcheck::DESCRIPTOR,
	&statix::DESCRIPTOR,
	&stylint::DESCRIPTOR,
	&swiftlint::DESCRIPTOR,
	&teal::DESCRIPTOR,
	&terraform_validate::DESCRIPTOR,
	&terragrunt_validate::DESCRIPTOR,
	&textidote::DESCRIPTOR,
	&textlint::DESCRIPTOR,
	&tfsec::DESCRIPTOR,
	&tidy::DESCRIPTOR,
	&trivy::DESCRIPTOR,
	&tsc::DESCRIPTOR,
	&twigcs::DESCRIPTOR,
	&vacuum::DESCRIPTOR,
	&vale::DESCRIPTOR,
	&verilator::DESCRIPTOR,
	&vint::DESCRIPTOR,
	&write_good::DESCRIPTOR,
	&yamllint::DESCRIPTOR,
	&zsh::DESCRIPTOR,
];

/// The answer of the tool parser `tool`; `None` when this module has none.
///
/// A tool parser recognized its tool's output when it found a finding, when
/// the tool exited 0 — a clean run may print a summary or nothing, and neither
/// is an unknown shape — or, for a JSON tool, when it found a JSON document at
/// all. What remains, a failed run whose output held nothing it reads, is not
/// recognized, and the core then hands the output to the sniffer.
pub fn answer(tool: &str, stdout: &[u8], stderr: &[u8], exit_code: i32) -> Option<Response> {
	crate::json_diag::begin_parse();
	let diags = dispatch(tool, stdout, stderr, exit_code)?;
	if diags.is_empty() && exit_code != 0 && !crate::json_diag::document_seen() {
		return Some(Response::unrecognized(tool));
	}
	Some(Response::recognized(tool, diags))
}

/// Dispatch a real tool parser by name. Returns `None` when this module has no
/// parser for `tool`, so the caller can fall back (echo / unknown).
pub fn dispatch(tool: &str, stdout: &[u8], stderr: &[u8], exit_code: i32) -> Option<Vec<RawDiagnostic>> {
	match tool {
		"actionlint" => Some(actionlint::parse(stdout, stderr, exit_code)),
		"alex" => Some(alex::parse(stdout, stderr, exit_code)),
		"ansiblelint" => Some(ansiblelint::parse(stdout, stderr, exit_code)),
		"bean_check" => Some(bean_check::parse(stdout, stderr, exit_code)),
		"bslint" => Some(bslint::parse(stdout, stderr, exit_code)),
		"buf" => Some(buf::parse(stdout, stderr, exit_code)),
		"buildifier" => Some(buildifier::parse(stdout, stderr, exit_code)),
		"cfn_lint" => Some(cfn_lint::parse(stdout, stderr, exit_code)),
		"checkmake" => Some(checkmake::parse(stdout, stderr, exit_code)),
		"checkstyle" => Some(checkstyle::parse(stdout, stderr, exit_code)),
		"clazy" => Some(clazy::parse(stdout, stderr, exit_code)),
		"clj_kondo" => Some(clj_kondo::parse(stdout, stderr, exit_code)),
		"cmake_lint" => Some(cmake_lint::parse(stdout, stderr, exit_code)),
		"codespell" => Some(codespell::parse(stdout, stderr, exit_code)),
		"commitlint" => Some(commitlint::parse(stdout, stderr, exit_code)),
		"cppcheck" => Some(cppcheck::parse(stdout, stderr, exit_code)),
		"credo" => Some(credo::parse(stdout, stderr, exit_code)),
		"cspell" => Some(cspell::parse(stdout, stderr, exit_code)),
		"cue_fmt" => Some(cue_fmt::parse(stdout, stderr, exit_code)),
		"dclint" => Some(dclint::parse(stdout, stderr, exit_code)),
		"deadnix" => Some(deadnix::parse(stdout, stderr, exit_code)),
		"djlint" => Some(djlint::parse(stdout, stderr, exit_code)),
		"dotenv_linter" => Some(dotenv_linter::parse(stdout, stderr, exit_code)),
		"droast" => Some(droast::parse(stdout, stderr, exit_code)),
		"editorconfig_checker" => Some(editorconfig_checker::parse(stdout, stderr, exit_code)),
		"erb_lint" => Some(erb_lint::parse(stdout, stderr, exit_code)),
		"eslint" => Some(eslint::parse(stdout, stderr, exit_code)),
		"fish" => Some(fish::parse(stdout, stderr, exit_code)),
		"gccdiag" => Some(gccdiag::parse(stdout, stderr, exit_code)),
		"gdlint" => Some(gdlint::parse(stdout, stderr, exit_code)),
		"gitleaks" => Some(gitleaks::parse(stdout, stderr, exit_code)),
		"gitlint" => Some(gitlint::parse(stdout, stderr, exit_code)),
		"glslc" => Some(glslc::parse(stdout, stderr, exit_code)),
		"golangci_lint" => Some(golangci_lint::parse(stdout, stderr, exit_code)),
		"hadolint" => Some(hadolint::parse(stdout, stderr, exit_code)),
		"haml_lint" => Some(haml_lint::parse(stdout, stderr, exit_code)),
		"harper_cli" => Some(harper_cli::parse(stdout, stderr, exit_code)),
		"knip" => Some(knip::parse(stdout, stderr, exit_code)),
		"ktlint" => Some(ktlint::parse(stdout, stderr, exit_code)),
		"kube_linter" => Some(kube_linter::parse(stdout, stderr, exit_code)),
		"ltrs" => Some(ltrs::parse(stdout, stderr, exit_code)),
		"markdownlint" => Some(markdownlint::parse(stdout, stderr, exit_code)),
		"markdownlint_cli2" => Some(markdownlint_cli2::parse(stdout, stderr, exit_code)),
		"markuplint" => Some(markuplint::parse(stdout, stderr, exit_code)),
		"mdl" => Some(mdl::parse(stdout, stderr, exit_code)),
		"mlint" => Some(mlint::parse(stdout, stderr, exit_code)),
		"mypy" => Some(mypy::parse(stdout, stderr, exit_code)),
		"npm_groovy_lint" => Some(npm_groovy_lint::parse(stdout, stderr, exit_code)),
		"opacheck" => Some(opacheck::parse(stdout, stderr, exit_code)),
		"opentofu_validate" => Some(opentofu_validate::parse(stdout, stderr, exit_code)),
		"perlimports" => Some(perlimports::parse(stdout, stderr, exit_code)),
		"phpcs" => Some(phpcs::parse(stdout, stderr, exit_code)),
		"phpmd" => Some(phpmd::parse(stdout, stderr, exit_code)),
		"phpstan" => Some(phpstan::parse(stdout, stderr, exit_code)),
		"pmd" => Some(pmd::parse(stdout, stderr, exit_code)),
		"proselint" => Some(proselint::parse(stdout, stderr, exit_code)),
		"protolint" => Some(protolint::parse(stdout, stderr, exit_code)),
		"puppet_lint" => Some(puppet_lint::parse(stdout, stderr, exit_code)),
		"pydoclint" => Some(pydoclint::parse(stdout, stderr, exit_code)),
		"pylint" => Some(pylint::parse(stdout, stderr, exit_code)),
		"qmllint" => Some(qmllint::parse(stdout, stderr, exit_code)),
		"reek" => Some(reek::parse(stdout, stderr, exit_code)),
		"regal" => Some(regal::parse(stdout, stderr, exit_code)),
		"revive" => Some(revive::parse(stdout, stderr, exit_code)),
		"rpmspec" => Some(rpmspec::parse(stdout, stderr, exit_code)),
		"rstcheck" => Some(rstcheck::parse(stdout, stderr, exit_code)),
		"rubocop" => Some(rubocop::parse(stdout, stderr, exit_code)),
		"saltlint" => Some(saltlint::parse(stdout, stderr, exit_code)),
		"selene" => Some(selene::parse(stdout, stderr, exit_code)),
		"semgrep" => Some(semgrep::parse(stdout, stderr, exit_code)),
		"solhint" => Some(solhint::parse(stdout, stderr, exit_code)),
		"spectral" => Some(spectral::parse(stdout, stderr, exit_code)),
		"sqlfluff" => Some(sqlfluff::parse(stdout, stderr, exit_code)),
		"sqruff" => Some(sqruff::parse(stdout, stderr, exit_code)),
		"staticcheck" => Some(staticcheck::parse(stdout, stderr, exit_code)),
		"statix" => Some(statix::parse(stdout, stderr, exit_code)),
		"stylint" => Some(stylint::parse(stdout, stderr, exit_code)),
		"swiftlint" => Some(swiftlint::parse(stdout, stderr, exit_code)),
		"teal" => Some(teal::parse(stdout, stderr, exit_code)),
		"terraform_validate" => Some(terraform_validate::parse(stdout, stderr, exit_code)),
		"terragrunt_validate" => Some(terragrunt_validate::parse(stdout, stderr, exit_code)),
		"textidote" => Some(textidote::parse(stdout, stderr, exit_code)),
		"textlint" => Some(textlint::parse(stdout, stderr, exit_code)),
		"tfsec" => Some(tfsec::parse(stdout, stderr, exit_code)),
		"tidy" => Some(tidy::parse(stdout, stderr, exit_code)),
		"trivy" => Some(trivy::parse(stdout, stderr, exit_code)),
		"tsc" => Some(tsc::parse(stdout, stderr, exit_code)),
		"twigcs" => Some(twigcs::parse(stdout, stderr, exit_code)),
		"vacuum" => Some(vacuum::parse(stdout, stderr, exit_code)),
		"vale" => Some(vale::parse(stdout, stderr, exit_code)),
		"verilator" => Some(verilator::parse(stdout, stderr, exit_code)),
		"vint" => Some(vint::parse(stdout, stderr, exit_code)),
		"write_good" => Some(write_good::parse(stdout, stderr, exit_code)),
		"yamllint" => Some(yamllint::parse(stdout, stderr, exit_code)),
		"zsh" => Some(zsh::parse(stdout, stderr, exit_code)),
		_ => None,
	}
}
