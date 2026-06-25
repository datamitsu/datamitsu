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
//! with that tool's parser, and is referenced from [`TOOLS`] below.

use crate::diagnostic::json_string;
use crate::tools;

/// Capabilities schema version. Bump on an incompatible shape change so the Go
/// decoder can refuse or adapt.
const SCHEMA_VERSION: u32 = 1;

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
}

/// The `echo` pipe-test parser's descriptor (defined here since `echo` lives in
/// the crate root, not in `tools`).
const ECHO: ToolCapability = ToolCapability {
    name: "echo",
    description: "Pipe-test parser: echoes stdout into a single diagnostic message and the \
        exit code into `code`. Proves the declare\u{2192}build\u{2192}sign\u{2192}deliver\u{2192}load\u{2192}invoke \
        pipe end to end; not a real tool.",
    url: "",
    operations: &[],
};

/// The capability table: the pipe-test `echo` plus one entry per real tool. A new
/// tool adds its module's `DESCRIPTOR` here (and a `tools::dispatch` arm).
const TOOLS: &[&ToolCapability] = &[
    &ECHO,
    &tools::actionlint::DESCRIPTOR,
    &tools::alex::DESCRIPTOR,
    &tools::ansiblelint::DESCRIPTOR,
    &tools::bean_check::DESCRIPTOR,
    &tools::bslint::DESCRIPTOR,
    &tools::buf::DESCRIPTOR,
    &tools::buildifier::DESCRIPTOR,
    &tools::cfn_lint::DESCRIPTOR,
    &tools::checkmake::DESCRIPTOR,
    &tools::checkstyle::DESCRIPTOR,
    &tools::clazy::DESCRIPTOR,
    &tools::clj_kondo::DESCRIPTOR,
    &tools::cmake_lint::DESCRIPTOR,
    &tools::codespell::DESCRIPTOR,
    &tools::commitlint::DESCRIPTOR,
    &tools::cppcheck::DESCRIPTOR,
    &tools::credo::DESCRIPTOR,
    &tools::cue_fmt::DESCRIPTOR,
    &tools::deadnix::DESCRIPTOR,
    &tools::djlint::DESCRIPTOR,
    &tools::dotenv_linter::DESCRIPTOR,
    &tools::editorconfig_checker::DESCRIPTOR,
    &tools::erb_lint::DESCRIPTOR,
    &tools::eslint::DESCRIPTOR,
    &tools::fish::DESCRIPTOR,
    &tools::gccdiag::DESCRIPTOR,
    &tools::gdlint::DESCRIPTOR,
    &tools::gitleaks::DESCRIPTOR,
    &tools::gitlint::DESCRIPTOR,
    &tools::glslc::DESCRIPTOR,
    &tools::golangci_lint::DESCRIPTOR,
    &tools::hadolint::DESCRIPTOR,
    &tools::haml_lint::DESCRIPTOR,
    &tools::ktlint::DESCRIPTOR,
    &tools::kube_linter::DESCRIPTOR,
    &tools::ltrs::DESCRIPTOR,
    &tools::markdownlint::DESCRIPTOR,
    &tools::markdownlint_cli2::DESCRIPTOR,
    &tools::markuplint::DESCRIPTOR,
    &tools::mdl::DESCRIPTOR,
    &tools::mlint::DESCRIPTOR,
    &tools::mypy::DESCRIPTOR,
    &tools::npm_groovy_lint::DESCRIPTOR,
    &tools::opacheck::DESCRIPTOR,
    &tools::opentofu_validate::DESCRIPTOR,
    &tools::perlimports::DESCRIPTOR,
    &tools::phpcs::DESCRIPTOR,
    &tools::phpmd::DESCRIPTOR,
    &tools::phpstan::DESCRIPTOR,
    &tools::pmd::DESCRIPTOR,
    &tools::proselint::DESCRIPTOR,
    &tools::protolint::DESCRIPTOR,
    &tools::puppet_lint::DESCRIPTOR,
    &tools::pydoclint::DESCRIPTOR,
    &tools::pylint::DESCRIPTOR,
    &tools::qmllint::DESCRIPTOR,
    &tools::reek::DESCRIPTOR,
    &tools::regal::DESCRIPTOR,
    &tools::revive::DESCRIPTOR,
    &tools::rpmspec::DESCRIPTOR,
    &tools::rstcheck::DESCRIPTOR,
    &tools::rubocop::DESCRIPTOR,
    &tools::saltlint::DESCRIPTOR,
    &tools::selene::DESCRIPTOR,
    &tools::semgrep::DESCRIPTOR,
    &tools::solhint::DESCRIPTOR,
    &tools::spectral::DESCRIPTOR,
    &tools::sqlfluff::DESCRIPTOR,
    &tools::sqruff::DESCRIPTOR,
    &tools::staticcheck::DESCRIPTOR,
    &tools::statix::DESCRIPTOR,
    &tools::stylint::DESCRIPTOR,
    &tools::swiftlint::DESCRIPTOR,
    &tools::teal::DESCRIPTOR,
    &tools::terraform_validate::DESCRIPTOR,
    &tools::terragrunt_validate::DESCRIPTOR,
    &tools::textidote::DESCRIPTOR,
    &tools::textlint::DESCRIPTOR,
    &tools::tfsec::DESCRIPTOR,
    &tools::tidy::DESCRIPTOR,
    &tools::trivy::DESCRIPTOR,
    &tools::twigcs::DESCRIPTOR,
    &tools::vacuum::DESCRIPTOR,
    &tools::vale::DESCRIPTOR,
    &tools::verilator::DESCRIPTOR,
    &tools::vint::DESCRIPTOR,
    &tools::write_good::DESCRIPTOR,
    &tools::yamllint::DESCRIPTOR,
    &tools::zsh::DESCRIPTOR,
];

/// Serialize the module's full capability manifest to JSON.
pub fn describe_json() -> String {
    let tools: Vec<String> = TOOLS.iter().map(|&t| tool_json(t)).collect();
    format!(
        r#"{{"schemaVersion":{},"module":{},"version":{},"tools":[{}]}}"#,
        SCHEMA_VERSION,
        json_string("datamitsu-parsers"),
        json_string(module_version()),
        tools.join(","),
    )
}

fn tool_json(t: &ToolCapability) -> String {
    let ops: Vec<String> = t.operations.iter().map(operation_json).collect();
    format!(
        r#"{{"name":{},"description":{},"url":{},"operations":{{{}}}}}"#,
        json_string(t.name),
        json_string(t.description),
        json_string(t.url),
        ops.join(","),
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
        assert!(json.contains(r#""schemaVersion":1"#), "json: {json}");
        assert!(json.contains(r#""module":"datamitsu-parsers""#), "json: {json}");
    }

    #[test]
    fn describe_lists_echo_and_real_tools() {
        let json = describe_json();
        for name in ["echo", "yamllint", "dotenv_linter", "cue_fmt"] {
            assert!(json.contains(&format!(r#""name":"{name}""#)), "missing {name}: {json}");
        }
    }

    #[test]
    fn describe_includes_an_invocation_recipe() {
        // yamllint advertises how to run it (parsable, stdin).
        let json = describe_json();
        assert!(json.contains(r#""lint":{"args":["--format","parsable","-"],"stdin":true}"#), "json: {json}");
    }

    #[test]
    fn module_version_is_never_empty() {
        assert!(!module_version().is_empty());
    }
}
