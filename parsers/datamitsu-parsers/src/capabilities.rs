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

use crate::diagnostic::json_string;

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
struct Operation {
    /// Operation the recipe is for (e.g. "lint").
    mode: &'static str,
    /// Args to pass the tool to produce output this parser understands.
    /// `{file}` is the per-file placeholder the core substitutes.
    args: &'static [&'static str],
    /// Whether the file content is fed on stdin rather than as a path arg.
    stdin: bool,
}

/// One tool this module knows how to parse.
struct ToolCapability {
    /// Dispatch name — the `parse` match arm and the value of `tool.outputParser`.
    name: &'static str,
    /// Human-readable description of the upstream tool.
    description: &'static str,
    /// Upstream tool URL, so a reader knows exactly what this parser targets.
    /// Empty for internal/pipe-test parsers.
    url: &'static str,
    /// Recommended invocations per mode; empty when the parser ships no canonical
    /// recipe yet.
    operations: &'static [Operation],
}

/// The capability table. Phase 1 ships only the `echo` pipe-test parser; each
/// real tool added to `dispatch` adds a row here (and to the manifest's names).
const TOOLS: &[ToolCapability] = &[ToolCapability {
    name: "echo",
    description: "Pipe-test parser: echoes stdout into a single diagnostic message and the \
        exit code into `code`. Proves the declare\u{2192}build\u{2192}sign\u{2192}deliver\u{2192}load\u{2192}invoke \
        pipe end to end; not a real tool.",
    url: "",
    operations: &[],
}];

/// Serialize the module's full capability manifest to JSON.
pub fn describe_json() -> String {
    let tools: Vec<String> = TOOLS.iter().map(tool_json).collect();
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
    fn describe_lists_echo_with_empty_operations() {
        let json = describe_json();
        assert!(json.contains(r#""name":"echo""#), "json: {json}");
        assert!(json.contains(r#""operations":{}"#), "echo ships no recipe: {json}");
    }

    #[test]
    fn module_version_is_never_empty() {
        // Build-injected (DATAMITSU_PARSERS_VERSION) or the crate-version fallback —
        // either way `describe` always reports a non-empty version.
        assert!(!module_version().is_empty());
    }
}
