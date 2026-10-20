use std::fs::{File, OpenOptions};
use std::io::{self, Read};
use std::path::{Component, Path};

use flate2::read::GzDecoder;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ArchiveKind {
	TarGz,
	Zip,
}

impl ArchiveKind {
	pub fn from_file_name(name: &str) -> Result<Self, String> {
		if name.ends_with(".tar.gz") {
			Ok(Self::TarGz)
		} else if name.ends_with(".zip") {
			Ok(Self::Zip)
		} else {
			Err(format!("{name} is neither a .tar.gz nor a .zip archive"))
		}
	}
}

/// Copies the regular file named binary at the archive's root to destination,
/// which must not exist yet. Every other entry is ignored, so no archive path
/// can place a file anywhere else.
pub fn extract_binary(archive: &Path, kind: ArchiveKind, binary: &str, destination: &Path) -> Result<(), String> {
	let file = File::open(archive).map_err(|err| format!("open {}: {err}", archive.display()))?;
	let found = match kind {
		ArchiveKind::TarGz => extract_from_tar_gz(file, binary, destination),
		ArchiveKind::Zip => extract_from_zip(file, binary, destination),
	}
	.map_err(|err| format!("extract {binary} from {}: {err}", file_name(archive)))?;
	if found {
		Ok(())
	} else {
		Err(format!("{} does not contain {binary}", file_name(archive)))
	}
}

fn extract_from_tar_gz(file: File, binary: &str, destination: &Path) -> io::Result<bool> {
	let mut archive = tar::Archive::new(GzDecoder::new(file));
	for entry in archive.entries()? {
		let mut entry = entry?;
		if entry.header().entry_type().is_file() && is_root_entry(&entry.path()?, binary) {
			write_new(destination, &mut entry)?;
			return Ok(true);
		}
	}
	Ok(false)
}

fn extract_from_zip(file: File, binary: &str, destination: &Path) -> io::Result<bool> {
	let mut archive = zip::ZipArchive::new(file).map_err(io::Error::other)?;
	for index in 0..archive.len() {
		let mut entry = archive.by_index(index).map_err(io::Error::other)?;
		if entry.is_file() && is_root_entry(Path::new(entry.name()), binary) {
			write_new(destination, &mut entry)?;
			return Ok(true);
		}
	}
	Ok(false)
}

fn is_root_entry(path: &Path, binary: &str) -> bool {
	let mut components = path.components().filter(|component| *component != Component::CurDir);
	matches!(
		(components.next(), components.next()),
		(Some(Component::Normal(name)), None) if name == binary
	)
}

fn write_new(destination: &Path, reader: &mut impl Read) -> io::Result<()> {
	let mut file = OpenOptions::new().write(true).create_new(true).open(destination)?;
	io::copy(reader, &mut file)?;
	file.sync_all()
}

fn file_name(path: &Path) -> String {
	path.file_name().map_or_else(
		|| path.display().to_string(),
		|name| name.to_string_lossy().into_owned(),
	)
}

#[cfg(test)]
pub(crate) mod tests {
	use std::io::Write;

	use flate2::Compression;
	use flate2::write::GzEncoder;

	use super::*;

	pub fn tar_gz(entries: &[(&str, &[u8])]) -> Vec<u8> {
		let mut builder = tar::Builder::new(GzEncoder::new(Vec::new(), Compression::default()));
		for (name, content) in entries {
			let mut header = tar::Header::new_gnu();
			header.set_size(content.len() as u64);
			header.set_mode(0o755);
			header.set_cksum();
			builder.append_data(&mut header, name, *content).unwrap();
		}
		builder.into_inner().unwrap().finish().unwrap()
	}

	pub fn zip(entries: &[(&str, &[u8])]) -> Vec<u8> {
		let mut writer = zip::ZipWriter::new(io::Cursor::new(Vec::new()));
		let options = zip::write::SimpleFileOptions::default().compression_method(zip::CompressionMethod::Deflated);
		for (name, content) in entries {
			writer.start_file(*name, options).unwrap();
			writer.write_all(content).unwrap();
		}
		writer.finish().unwrap().into_inner()
	}

	fn write(dir: &Path, name: &str, bytes: &[u8]) -> std::path::PathBuf {
		let path = dir.join(name);
		std::fs::write(&path, bytes).unwrap();
		path
	}

	#[test]
	fn extracts_the_root_binary_from_a_tar_gz() {
		let dir = tempfile::tempdir().unwrap();
		let archive = write(
			dir.path(),
			"a.tar.gz",
			&tar_gz(&[
				("LICENSE", b"license"),
				("completions/datamitsu", b"not the binary"),
				("./datamitsu", b"binary"),
			]),
		);
		let destination = dir.path().join("out");
		extract_binary(&archive, ArchiveKind::TarGz, "datamitsu", &destination).unwrap();
		assert_eq!(std::fs::read(destination).unwrap(), b"binary");
	}

	#[test]
	fn extracts_the_root_binary_from_a_zip() {
		let dir = tempfile::tempdir().unwrap();
		let archive = write(
			dir.path(),
			"a.zip",
			&zip(&[
				("completions/datamitsu.exe", b"not the binary"),
				("datamitsu.exe", b"binary"),
			]),
		);
		let destination = dir.path().join("out.exe");
		extract_binary(&archive, ArchiveKind::Zip, "datamitsu.exe", &destination).unwrap();
		assert_eq!(std::fs::read(destination).unwrap(), b"binary");
	}

	#[test]
	fn ignores_nested_and_traversing_entries() {
		let dir = tempfile::tempdir().unwrap();
		for (name, bytes, kind) in [
			(
				"nested.tar.gz",
				tar_gz(&[("bin/datamitsu", b"nested")]),
				ArchiveKind::TarGz,
			),
			("nested.zip", zip(&[("../datamitsu", b"escape")]), ArchiveKind::Zip),
		] {
			let archive = write(dir.path(), name, &bytes);
			let destination = dir.path().join(format!("{name}.out"));
			let err = extract_binary(&archive, kind, "datamitsu", &destination).unwrap_err();
			assert!(err.ends_with("does not contain datamitsu"), "{err}");
			assert!(!destination.exists());
		}
	}

	#[test]
	fn never_overwrites_an_existing_destination() {
		let dir = tempfile::tempdir().unwrap();
		let archive = write(dir.path(), "a.tar.gz", &tar_gz(&[("datamitsu", b"binary")]));
		let destination = write(dir.path(), "out", b"existing");
		assert!(extract_binary(&archive, ArchiveKind::TarGz, "datamitsu", &destination).is_err());
		assert_eq!(std::fs::read(destination).unwrap(), b"existing");
	}

	#[test]
	fn infers_the_archive_kind_from_the_file_name() {
		assert_eq!(ArchiveKind::from_file_name("a.tar.gz"), Ok(ArchiveKind::TarGz));
		assert_eq!(ArchiveKind::from_file_name("a.zip"), Ok(ArchiveKind::Zip));
		assert!(ArchiveKind::from_file_name("a.tar.xz").is_err());
	}
}
