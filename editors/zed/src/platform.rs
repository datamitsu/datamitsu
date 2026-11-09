use zed_extension_api::{Architecture, Os};

/// A datamitsu release target, named as `GoReleaser` names its archives.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Target {
	goos: &'static str,
	goarch: &'static str,
}

impl Target {
	pub fn from_platform(os: Os, arch: Architecture) -> Result<Self, String> {
		let goos = match os {
			Os::Mac => "darwin",
			Os::Linux => "linux",
			Os::Windows => "windows",
		};
		let goarch = match arch {
			Architecture::Aarch64 => "arm64",
			Architecture::X8664 => "amd64",
			Architecture::X86 => return Err(format!("datamitsu publishes no release for 32-bit x86 ({goos})")),
		};
		Ok(Self { goos, goarch })
	}

	/// The key the pinned release table uses: `<goos>_<goarch>`.
	pub fn key(&self) -> String {
		format!("{}_{}", self.goos, self.goarch)
	}

	pub fn binary_name(&self) -> &'static str {
		if self.goos == "windows" {
			"datamitsu.exe"
		} else {
			"datamitsu"
		}
	}
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn maps_zed_platforms_to_release_targets() {
		let cases = [
			(Os::Mac, Architecture::Aarch64, "darwin_arm64", "datamitsu"),
			(Os::Mac, Architecture::X8664, "darwin_amd64", "datamitsu"),
			(Os::Linux, Architecture::Aarch64, "linux_arm64", "datamitsu"),
			(Os::Linux, Architecture::X8664, "linux_amd64", "datamitsu"),
			(Os::Windows, Architecture::Aarch64, "windows_arm64", "datamitsu.exe"),
			(Os::Windows, Architecture::X8664, "windows_amd64", "datamitsu.exe"),
		];
		for (os, arch, key, binary) in cases {
			let target = Target::from_platform(os, arch).unwrap();
			assert_eq!(target.key(), key);
			assert_eq!(target.binary_name(), binary);
		}
	}

	#[test]
	fn rejects_32_bit_x86() {
		let err = Target::from_platform(Os::Linux, Architecture::X86).unwrap_err();
		assert!(err.contains("32-bit x86"), "{err}");
	}
}
