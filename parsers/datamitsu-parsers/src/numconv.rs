//! Shared numeric coercions for parsing tool output.
//!
//! Tool diagnostics arrive as JSON numbers (always `f64`) or numeric strings,
//! and must become `u32` line/column positions or small integer severity tokens.
//! A bare `n as u32` / `n as i64` cast is lossy at the edges: Rust *saturates*
//! out-of-range values (e.g. `5e9 as u32` → `u32::MAX`) and silently truncates
//! the fractional part (`2.5 as i64` → `2`). Both turn malformed input into a
//! plausible-but-wrong value instead of "absent". These helpers reject those
//! cases up front so every parser treats out-of-range / non-integer numbers as
//! `None`, the single place this policy lives.

/// Convert a JSON number to a `u32` line/column, accepting only a finite,
/// non-negative integer within `u32` range; anything else is `None` (absent)
/// rather than a saturated `u32::MAX`.
pub fn json_u32(n: f64) -> Option<u32> {
	if n.is_finite() && n >= 0.0 && n <= u32::MAX as f64 {
		Some(n as u32)
	} else {
		None
	}
}

/// Convert a JSON number to an integer (e.g. a severity token), rejecting
/// non-finite or non-integer values so `2.5` does not collapse onto `2`.
pub fn json_int(n: f64) -> Option<i64> {
	if n.is_finite() && n.fract() == 0.0 {
		Some(n as i64)
	} else {
		None
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn json_u32_accepts_in_range_integers() {
		assert_eq!(json_u32(0.0), Some(0));
		assert_eq!(json_u32(42.0), Some(42));
		assert_eq!(json_u32(u32::MAX as f64), Some(u32::MAX));
	}

	#[test]
	fn json_u32_rejects_out_of_range_and_negative() {
		assert_eq!(json_u32(-1.0), None);
		assert_eq!(json_u32(u32::MAX as f64 + 1.0), None); // no saturation to u32::MAX
		assert_eq!(json_u32(9_999_999_999.0), None);
		assert_eq!(json_u32(f64::NAN), None);
		assert_eq!(json_u32(f64::INFINITY), None);
	}

	#[test]
	fn json_int_rejects_non_integers() {
		assert_eq!(json_int(2.0), Some(2));
		assert_eq!(json_int(2.5), None); // does not truncate to 2
		assert_eq!(json_int(-3.0), Some(-3));
		assert_eq!(json_int(f64::NAN), None);
	}
}
