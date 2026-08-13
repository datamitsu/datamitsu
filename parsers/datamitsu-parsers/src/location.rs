//! Splitting the `<file>:<row>:<col>` location prefix that most line-oriented
//! tools print.
//!
//! Read **right-to-left**: row and column are the last fields, so whatever
//! precedes them is the path — including a path that itself contains colons
//! (`weird:name.yaml:12:4`) or no path at all (`12:4`, which a tool emits when it
//! read from stdin, or when the "path" was consumed by an earlier delimiter).
//!
//! Splitting from the right rather than subtracting token lengths is deliberate:
//! the arithmetic form wraps around below zero on a location with no path, and in
//! a `panic = "abort"` WASM build that traps the module — the host then loses
//! **every** diagnostic of the run, not the one malformed line.

/// Split `<file>:<row>:<col>`. The file is `""` when the location carries none.
pub fn file_row_col(loc: &str) -> Option<(&str, u32, u32)> {
	let mut it = loc.rsplitn(3, ':');
	let col: u32 = it.next()?.trim().parse().ok()?;
	let row: u32 = it.next()?.trim().parse().ok()?;
	Some((it.next().unwrap_or(""), row, col))
}

/// Split `<file>:<row>` for the tools that report no column.
pub fn file_row(loc: &str) -> Option<(&str, u32)> {
	let mut it = loc.rsplitn(2, ':');
	let row: u32 = it.next()?.trim().parse().ok()?;
	Some((it.next().unwrap_or(""), row))
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn splits_a_plain_location() {
		assert_eq!(file_row_col("src/a.ts:12:4"), Some(("src/a.ts", 12, 4)));
		assert_eq!(file_row("config/.env:7"), Some(("config/.env", 7)));
	}

	#[test]
	fn keeps_colons_inside_the_path() {
		assert_eq!(file_row_col("weird:name.yaml:12:4"), Some(("weird:name.yaml", 12, 4)));
		assert_eq!(file_row_col("/abs/c:/x.md:1:2"), Some(("/abs/c:/x.md", 1, 2)));
	}

	#[test]
	fn a_location_without_a_path_yields_an_empty_file_not_a_panic() {
		// The case the length arithmetic used to wrap around on.
		assert_eq!(file_row_col("12:30"), Some(("", 12, 30)));
		assert_eq!(file_row("30"), Some(("", 30)));
	}

	#[test]
	fn rejects_non_numeric_positions() {
		assert_eq!(file_row_col("src/a.ts:x:4"), None);
		assert_eq!(file_row_col("nope"), None);
		assert_eq!(file_row_col(""), None);
		assert_eq!(file_row(""), None);
	}
}
