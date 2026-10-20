use std::fmt::Write as _;
use std::fs::File;
use std::io::Read;
use std::path::Path;

use sha2::{Digest, Sha256};

pub fn validate_sha256(value: &str) -> Result<(), String> {
	if let Some(hex) = value.strip_prefix("sha256:")
		&& hex.len() == 64
		&& hex
			.bytes()
			.all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
	{
		return Ok(());
	}
	Err("expected sha256:<64 lowercase hex> digest".to_string())
}

pub fn verify_sha256(path: &Path, expected: &str) -> Result<(), String> {
	validate_sha256(expected)?;
	let mut file = File::open(path).map_err(|err| format!("open {}: {err}", path.display()))?;
	let mut hasher = Sha256::new();
	let mut buffer = [0; 64 * 1024];
	loop {
		let read = file
			.read(&mut buffer)
			.map_err(|err| format!("read {}: {err}", path.display()))?;
		if read == 0 {
			break;
		}
		hasher.update(&buffer[..read]);
	}
	let actual = canonical_sha256(&hasher.finalize());
	if actual == expected {
		Ok(())
	} else {
		Err(format!(
			"SHA-256 mismatch for {}: expected {expected}, got {actual}",
			path.file_name().unwrap_or_default().to_string_lossy()
		))
	}
}

fn canonical_sha256(bytes: &[u8]) -> String {
	let mut out = String::with_capacity("sha256:".len() + bytes.len() * 2);
	out.push_str("sha256:");
	for byte in bytes {
		let _ = write!(out, "{byte:02x}");
	}
	out
}

#[cfg(test)]
pub fn sha256(bytes: &[u8]) -> String {
	canonical_sha256(&Sha256::digest(bytes))
}

#[cfg(test)]
mod tests {
	use super::*;

	const PAYLOAD_HASH: &str = "sha256:239f59ed55e737c77147cf55ad0c1b030b6d7ee748a7426952f9b852d5a935e5";

	#[test]
	fn verifies_a_matching_digest() {
		let dir = tempfile::tempdir().unwrap();
		let path = dir.path().join("archive.tar.gz");
		std::fs::write(&path, b"payload").unwrap();
		verify_sha256(&path, PAYLOAD_HASH).unwrap();
	}

	#[test]
	fn rejects_invalid_pins_before_opening_the_file() {
		for pin in [
			"".to_string(),
			"a".repeat(64),
			format!("sha256:{}", "A".repeat(64)),
			format!("sha256:{}", "z".repeat(64)),
			format!("sha256:{}", "a".repeat(63)),
			format!("xxh3:{}", "a".repeat(32)),
		] {
			let err = verify_sha256(Path::new("missing-archive"), &pin).unwrap_err();
			assert_eq!(err, "expected sha256:<64 lowercase hex> digest", "{pin}");
		}
	}

	#[test]
	fn rejects_a_mismatched_digest_with_canonical_values() {
		let dir = tempfile::tempdir().unwrap();
		let path = dir.path().join("archive.tar.gz");
		std::fs::write(&path, b"payload").unwrap();
		let expected = sha256(b"other");
		let err = verify_sha256(&path, &expected).unwrap_err();
		assert_eq!(
			err,
			format!("SHA-256 mismatch for archive.tar.gz: expected {expected}, got {PAYLOAD_HASH}")
		);
	}
}
