//! Normalized severity scale shared by parsers: 1=error … 4=hint.
//!
//! This matches both none-ls's `severities` table and LSP `DiagnosticSeverity`
//! (1=Error, 2=Warning, 3=Information, 4=Hint), so a parser emits a stable level
//! the Go core maps 1:1. A tool that reports no level leaves `severity` as `None`,
//! and the core supplies its fallback.

pub const ERROR: u8 = 1;
pub const WARNING: u8 = 2;
// INFO/HINT complete the scale; the first tools (yamllint/cue_fmt/dotenv-linter)
// only emit error/warning, so these stay unused until a tool that maps to them
// (e.g. hadolint's info→INFO, style→HINT) is added.
#[allow(dead_code)]
pub const INFO: u8 = 3;
#[allow(dead_code)]
pub const HINT: u8 = 4;
