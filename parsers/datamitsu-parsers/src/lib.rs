//! datamitsu WASM parser modules.
//!
//! A parser turns a third-party tool's raw text output into structured
//! diagnostics. The architectural invariant (Phase 1): **a parser extracts only
//! what the tool actually emitted** — every field but `message` is optional, and
//! `None` means "the tool did not provide this", not an error. The Go core fills
//! defaults later; the module never invents data.
//!
//! Host call contract:
//!   1. host calls `alloc(len)` for each input buffer (tool name, stdout, stderr)
//!      and writes the bytes,
//!   2. host calls `parse(...)` passing the ptr/len of each buffer + exit code,
//!   3. `parse` returns a single u64 packing `(ptr << 32) | len` of a freshly
//!      allocated UTF-8 JSON output buffer,
//!   4. host reads the output then calls `dealloc(ptr, len)` to free it (and frees
//!      the input buffers the same way).
//!
//! Raw bytes are delivered **whole, never host-line-split** (analysis.md §2.3):
//! line-splitting in the host loses multiline cases like cue_fmt, so the parser
//! decides whether to split.

use std::alloc::{self, Layout};
use std::ptr;

mod capabilities;
mod diagnostic;
mod severity;
mod tools;

pub use capabilities::describe_json;
pub use diagnostic::RawDiagnostic;

/// Allocate `len` bytes in the module's linear memory and return a pointer the
/// host can write into. Ownership transfers to the host until it calls
/// [`dealloc`]. We allocate an exact `Layout` (size = `len`, align = 1) so the
/// matching `dealloc` can reconstruct the identical layout — unlike a `Vec`,
/// whose backing capacity may exceed `len` and would make the free layout
/// ambiguous.
#[no_mangle]
pub extern "C" fn alloc(len: u32) -> *mut u8 {
    if len == 0 {
        return ptr::null_mut();
    }
    let layout = Layout::from_size_align(len as usize, 1).expect("valid layout");
    // SAFETY: len > 0, so the layout is non-zero-sized.
    unsafe { alloc::alloc(layout) }
}

/// Free a buffer previously returned by [`alloc`] (or by [`parse`]'s output).
/// `len` must match the original allocation length so the freed `Layout` is
/// identical to the allocated one.
///
/// # Safety
/// `ptr`/`len` must describe a buffer obtained from this module's `alloc`.
#[no_mangle]
pub unsafe extern "C" fn dealloc(ptr: *mut u8, len: u32) {
    if ptr.is_null() || len == 0 {
        return;
    }
    let layout = Layout::from_size_align(len as usize, 1).expect("valid layout");
    alloc::dealloc(ptr, layout);
}

/// Dispatcher. Receives the tool name and the tool's raw stdout/stderr bytes
/// plus its exit code, and returns the JSON-serialized parse result packed as
/// `(ptr << 32) | len`.
///
/// # Safety
/// Every `*_ptr`/`*_len` pair must describe a buffer obtained from `alloc`.
#[no_mangle]
pub unsafe extern "C" fn parse(
    tool_ptr: *const u8,
    tool_len: u32,
    stdout_ptr: *const u8,
    stdout_len: u32,
    stderr_ptr: *const u8,
    stderr_len: u32,
    exit_code: i32,
) -> u64 {
    let tool = slice(tool_ptr, tool_len);
    let stdout = slice(stdout_ptr, stdout_len);
    let stderr = slice(stderr_ptr, stderr_len);

    let tool_name = std::str::from_utf8(tool).unwrap_or("");
    let json = dispatch(tool_name, stdout, stderr, exit_code);
    leak_json(json)
}

/// Describe the module's capabilities: the tools it parses, how to invoke each,
/// and its build-injected version. Takes no input; returns `(ptr << 32) | len` of
/// a freshly allocated UTF-8 JSON buffer the host reads then frees via `dealloc`.
/// Static counterpart to [`parse`] — the host can introspect a module without
/// running any tool (`datamitsu devtools parsers list`).
#[no_mangle]
pub extern "C" fn describe() -> u64 {
    leak_json(capabilities::describe_json())
}

unsafe fn slice<'a>(ptr: *const u8, len: u32) -> &'a [u8] {
    if ptr.is_null() || len == 0 {
        &[]
    } else {
        std::slice::from_raw_parts(ptr, len as usize)
    }
}

/// Pack a string into linear memory and return `(ptr << 32) | len`. The bytes
/// are copied into an exact-`Layout` allocation (matching [`alloc`]) so the
/// host's `dealloc(ptr, len)` frees the identical layout.
fn leak_json(s: String) -> u64 {
    let bytes = s.into_bytes();
    let len = bytes.len();
    if len == 0 {
        return 0;
    }
    let layout = Layout::from_size_align(len, 1).expect("valid layout");
    // SAFETY: len > 0; copy exactly len bytes into the fresh allocation.
    let ptr = unsafe { alloc::alloc(layout) };
    if ptr.is_null() {
        return 0;
    }
    unsafe {
        ptr::copy_nonoverlapping(bytes.as_ptr(), ptr, len);
    }
    ((ptr as u64) << 32) | len as u64
}

/// Pure dispatch core — split out so native `cargo test` can exercise it without
/// the pointer ABI. Phase 1 only knows the `echo` parser used to prove the pipe;
/// Phase 2 adds one `match` arm + one module fn per real tool.
pub fn dispatch(tool: &str, stdout: &[u8], stderr: &[u8], exit_code: i32) -> String {
    // Real tool parsers live one-per-module under `tools`; try them first.
    if let Some(diags) = tools::dispatch(tool, stdout, stderr, exit_code) {
        return diagnostic::to_json_array(&diags);
    }
    match tool {
        "echo" => echo(stdout, stderr, exit_code),
        // Unknown tool: an empty diagnostic list (not an error — the core decides
        // how to treat "no parser produced anything").
        _ => "[]".to_string(),
    }
}

/// The `echo` parser: a trivial, deterministic branch used only to prove the
/// declare→build→sign→deliver→load→invoke pipe end to end. It echoes the raw
/// stdout (lossily decoded) back as a single diagnostic `message` and records the
/// exit code in `code`. Every other field stays `None` to model the nullable
/// contract.
fn echo(stdout: &[u8], _stderr: &[u8], exit_code: i32) -> String {
    let message = String::from_utf8_lossy(stdout).into_owned();
    let diag = RawDiagnostic {
        message,
        code: Some(exit_code.to_string()),
        ..RawDiagnostic::default()
    };
    diagnostic::to_json_array(&[diag])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn echo_round_trips_stdout_into_message() {
        let json = dispatch("echo", b"hello world", b"", 0);
        assert_eq!(
            json, r#"[{"message":"hello world","code":"0"}]"#,
            "echo must echo stdout into message and exit code into code"
        );
    }

    #[test]
    fn echo_preserves_multiline_input_whole() {
        // The host must not line-split; a multiline payload stays one message.
        let json = dispatch("echo", b"line1\nline2", b"", 2);
        assert_eq!(json, r#"[{"message":"line1\nline2","code":"2"}]"#);
    }

    #[test]
    fn unknown_tool_returns_empty_array() {
        assert_eq!(dispatch("not-a-real-parser", b"anything", b"", 1), "[]");
    }

    #[test]
    fn alloc_dealloc_round_trips() {
        let ptr = alloc(16);
        assert!(!ptr.is_null());
        // Write through the pointer, then hand it back.
        unsafe {
            for i in 0..16u32 {
                *ptr.add(i as usize) = i as u8;
            }
            dealloc(ptr, 16);
        }
    }

    #[test]
    fn dealloc_null_is_noop() {
        unsafe { dealloc(std::ptr::null_mut(), 0) };
    }
}
