use std::fs;
use std::io;
use std::path::Path;

use crate::archive::{self, ArchiveKind};
use crate::digest;
use crate::platform::Target;
use crate::release::{Asset, Pins};

const INSTALL_PREFIX: &str = "datamitsu-";

/// What installing needs from Zed. Paths are relative to the extension's work
/// directory, the only place an extension may write.
pub trait Host {
	fn download(&self, url: &str, path: &str) -> Result<(), String>;
	fn make_executable(&self, path: &str) -> Result<(), String>;
	fn downloading(&self);
	fn done(&self);
}

/// Returns the pinned datamitsu binary under root, downloading, verifying and
/// unpacking it on first use. The result is relative to root.
///
/// The archive is downloaded as is and checked against its pinned SHA-256
/// before anything is unpacked: Zed's own unpacking would extract bytes nobody
/// has verified. The install is assembled in a staging directory and renamed
/// into place, so an interrupted one is never mistaken for a finished one.
pub fn install(root: &Path, pins: &Pins, target: &Target, host: &impl Host) -> Result<String, String> {
	let asset = pins.asset(target)?;
	let dir = format!("{INSTALL_PREFIX}{}", pins.version);
	let binary = format!("{dir}/{}", target.binary_name());
	if root.join(&binary).is_file() {
		return Ok(binary);
	}

	host.downloading();
	let staging = format!("{dir}.partial");
	remove_dir(&root.join(&staging))?;
	fs::create_dir_all(root.join(&staging)).map_err(|err| format!("create {staging}: {err}"))?;
	if let Err(err) = stage(root, pins, asset, target, &staging, host) {
		let _ = fs::remove_dir_all(root.join(&staging));
		return Err(err);
	}

	remove_dir(&root.join(&dir))?;
	fs::rename(root.join(&staging), root.join(&dir)).map_err(|err| format!("move {staging} to {dir}: {err}"))?;
	remove_other_installs(root, &dir);
	host.done();
	Ok(binary)
}

fn stage(
	root: &Path,
	pins: &Pins,
	asset: &Asset,
	target: &Target,
	staging: &str,
	host: &impl Host,
) -> Result<(), String> {
	let kind = ArchiveKind::from_file_name(&asset.file)?;
	let archive = format!("{staging}/{}", asset.file);
	host.download(&pins.url(asset), &archive)?;
	digest::verify_sha256(&root.join(&archive), &asset.hash)?;
	let binary = format!("{staging}/{}", target.binary_name());
	archive::extract_binary(&root.join(&archive), kind, target.binary_name(), &root.join(&binary))?;
	fs::remove_file(root.join(&archive)).map_err(|err| format!("remove {archive}: {err}"))?;
	host.make_executable(&binary)
}

fn remove_dir(path: &Path) -> Result<(), String> {
	match fs::remove_dir_all(path) {
		Err(err) if err.kind() != io::ErrorKind::NotFound => Err(format!("remove {}: {err}", path.display())),
		_ => Ok(()),
	}
}

/// Best effort: a leftover version only costs disk space.
fn remove_other_installs(root: &Path, keep: &str) {
	let Ok(entries) = fs::read_dir(root) else {
		return;
	};
	for entry in entries.flatten() {
		let name = entry.file_name();
		let name = name.to_string_lossy();
		if name.starts_with(INSTALL_PREFIX) && name != keep && entry.path().is_dir() {
			let _ = fs::remove_dir_all(entry.path());
		}
	}
}

#[cfg(test)]
mod tests {
	use std::cell::RefCell;
	use std::path::PathBuf;

	use zed_extension_api::{Architecture, Os};

	use super::*;
	use crate::archive::tests::tar_gz;
	use crate::digest::sha256;
	use crate::release;

	struct FakeHost {
		root: PathBuf,
		archive: Vec<u8>,
		calls: RefCell<Vec<String>>,
	}

	impl Host for FakeHost {
		fn download(&self, url: &str, path: &str) -> Result<(), String> {
			self.calls.borrow_mut().push(format!("download {url} -> {path}"));
			fs::write(self.root.join(path), &self.archive).map_err(|err| err.to_string())
		}

		fn make_executable(&self, path: &str) -> Result<(), String> {
			self.calls.borrow_mut().push(format!("chmod {path}"));
			Ok(())
		}

		fn downloading(&self) {
			self.calls.borrow_mut().push("downloading".to_string());
		}

		fn done(&self) {
			self.calls.borrow_mut().push("done".to_string());
		}
	}

	fn linux() -> Target {
		Target::from_platform(Os::Linux, Architecture::X8664).unwrap()
	}

	fn pins(version: &str, hash: &str) -> Pins {
		release::parse(&format!(
			r#"{{"version": "{version}", "assets": {{"linux_amd64": {{"file": "datamitsu_{version}_linux_amd64.tar.gz", "hash": "{hash}"}}}}}}"#
		))
		.unwrap()
	}

	fn host(root: &Path, archive: Vec<u8>) -> FakeHost {
		FakeHost {
			root: root.to_path_buf(),
			archive,
			calls: RefCell::new(Vec::new()),
		}
	}

	#[test]
	fn downloads_verifies_and_installs_the_pinned_binary() {
		let root = tempfile::tempdir().unwrap();
		let archive = tar_gz(&[("datamitsu", b"binary")]);
		let host = host(root.path(), archive.clone());

		let binary = install(root.path(), &pins("0.4.0", &sha256(&archive)), &linux(), &host).unwrap();

		assert_eq!(binary, "datamitsu-0.4.0/datamitsu");
		assert_eq!(fs::read(root.path().join(&binary)).unwrap(), b"binary");
		assert_eq!(
			*host.calls.borrow(),
			[
				"downloading",
				"download https://github.com/datamitsu/datamitsu/releases/download/v0.4.0/datamitsu_0.4.0_linux_amd64.tar.gz -> datamitsu-0.4.0.partial/datamitsu_0.4.0_linux_amd64.tar.gz",
				"chmod datamitsu-0.4.0.partial/datamitsu",
				"done",
			]
		);
		assert!(!root.path().join("datamitsu-0.4.0.partial").exists());
		assert!(
			!root
				.path()
				.join("datamitsu-0.4.0/datamitsu_0.4.0_linux_amd64.tar.gz")
				.exists()
		);
	}

	#[test]
	fn reuses_an_installed_binary_without_downloading() {
		let root = tempfile::tempdir().unwrap();
		fs::create_dir(root.path().join("datamitsu-0.4.0")).unwrap();
		fs::write(root.path().join("datamitsu-0.4.0/datamitsu"), b"binary").unwrap();
		let host = host(root.path(), Vec::new());

		let binary = install(
			root.path(),
			&pins("0.4.0", &format!("sha256:{}", "0".repeat(64))),
			&linux(),
			&host,
		)
		.unwrap();

		assert_eq!(binary, "datamitsu-0.4.0/datamitsu");
		assert!(host.calls.borrow().is_empty());
	}

	#[test]
	fn refuses_an_archive_that_does_not_match_its_pin() {
		let root = tempfile::tempdir().unwrap();
		let host = host(root.path(), tar_gz(&[("datamitsu", b"tampered")]));

		let err = install(root.path(), &pins("0.4.0", &sha256(b"expected")), &linux(), &host).unwrap_err();

		assert!(err.starts_with("SHA-256 mismatch"), "{err}");
		assert!(!root.path().join("datamitsu-0.4.0").exists());
		assert!(!root.path().join("datamitsu-0.4.0.partial").exists());
		assert!(!host.calls.borrow().iter().any(|call| call.starts_with("chmod")));
	}

	#[test]
	fn replaces_a_leftover_staging_directory_and_removes_other_versions() {
		let root = tempfile::tempdir().unwrap();
		fs::create_dir_all(root.path().join("datamitsu-0.4.0.partial")).unwrap();
		fs::write(root.path().join("datamitsu-0.4.0.partial/junk"), b"junk").unwrap();
		fs::create_dir_all(root.path().join("datamitsu-0.3.1")).unwrap();
		fs::create_dir_all(root.path().join("unrelated")).unwrap();
		let archive = tar_gz(&[("datamitsu", b"binary")]);
		let host = host(root.path(), archive.clone());

		install(root.path(), &pins("0.4.0", &sha256(&archive)), &linux(), &host).unwrap();

		assert!(!root.path().join("datamitsu-0.4.0/junk").exists());
		assert!(!root.path().join("datamitsu-0.3.1").exists());
		assert!(root.path().join("unrelated").exists());
	}
}
