//! Per-tool parsers. One module per tool, each exporting `parse` (raw tool output
//! → nullable `RawDiagnostic`s) and a `DESCRIPTOR` (its `describe` capability with
//! the recommended invocation). Adding a tool is: a new module here, one arm in
//! [`dispatch`], and one entry in `capabilities::TOOLS`.
//!
//! Parsers are **hand-written** (no `regex`/`serde` dependency) to keep the WASM
//! artifact tiny — the logic is ported faithfully from the upstream none-ls
//! builtin / efm errorformat for each tool.

// Shared helper for the JSON-output tool class (not a tool itself).
pub mod json_diag;

pub mod cue_fmt;
pub mod dotenv_linter;
pub mod hadolint;
pub mod yamllint;

use crate::diagnostic::RawDiagnostic;

/// Dispatch a real tool parser by name. Returns `None` when this module has no
/// parser for `tool`, so the caller can fall back (echo / unknown).
pub fn dispatch(tool: &str, stdout: &[u8], stderr: &[u8], exit_code: i32) -> Option<Vec<RawDiagnostic>> {
    match tool {
        "cue_fmt" => Some(cue_fmt::parse(stdout, stderr, exit_code)),
        "dotenv_linter" => Some(dotenv_linter::parse(stdout, stderr, exit_code)),
        "hadolint" => Some(hadolint::parse(stdout, stderr, exit_code)),
        "yamllint" => Some(yamllint::parse(stdout, stderr, exit_code)),
        _ => None,
    }
}
