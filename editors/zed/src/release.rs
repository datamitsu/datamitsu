use std::collections::BTreeMap;

use serde::Deserialize;
use zed_extension_api::serde_json;

use crate::platform::Target;

const REPOSITORY: &str = "datamitsu/datamitsu";

/// Version of the placeholder the monorepo carries; a published build replaces
/// it with the release it pins.
const PLACEHOLDER_VERSION: &str = "0.0.0";

/// The datamitsu release this build of the extension downloads when no
/// datamitsu is on PATH, with the SHA-256 of each archive. Written at release
/// time by `scripts/gen-release.ts` from `GoReleaser`'s `checksums.txt`.
#[derive(Debug, Deserialize)]
pub struct Pins {
	pub version: String,
	#[serde(default)]
	assets: BTreeMap<String, Asset>,
}

#[derive(Debug, Deserialize)]
pub struct Asset {
	pub file: String,
	pub sha256: String,
}

pub fn pinned() -> Result<Pins, String> {
	parse(include_str!("../release.json"))
}

pub fn parse(json: &str) -> Result<Pins, String> {
	serde_json::from_str(json).map_err(|err| format!("release.json is malformed: {err}"))
}

impl Pins {
	/// The asset to download for target. Refuses a placeholder build and any
	/// asset without a well-formed SHA-256: nothing is downloaded unverified.
	pub fn asset(&self, target: &Target) -> Result<&Asset, String> {
		if self.version == PLACEHOLDER_VERSION {
			return Err(
				"This build of the extension pins no datamitsu release, so it cannot download one. \
			            Install datamitsu on PATH, or set lsp.datamitsu.binary.path \
			            (with \"arguments\": [\"lsp\"]) in your Zed settings."
					.to_string(),
			);
		}
		let key = target.key();
		let asset = self
			.assets
			.get(&key)
			.ok_or_else(|| format!("datamitsu {} pins no release asset for {key}", self.version))?;
		if !is_sha256(&asset.sha256) {
			return Err(format!("the pinned SHA-256 of {} is not a SHA-256 digest", asset.file));
		}
		Ok(asset)
	}

	pub fn url(&self, asset: &Asset) -> String {
		format!(
			"https://github.com/{REPOSITORY}/releases/download/v{}/{}",
			self.version, asset.file
		)
	}
}

fn is_sha256(value: &str) -> bool {
	value.len() == 64 && value.bytes().all(|byte| byte.is_ascii_hexdigit())
}

#[cfg(test)]
mod tests {
	use zed_extension_api::{Architecture, Os};

	use super::*;

	const DIGEST: &str = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

	fn linux() -> Target {
		Target::from_platform(Os::Linux, Architecture::X8664).unwrap()
	}

	#[test]
	fn the_committed_release_json_parses() {
		pinned().unwrap();
	}

	#[test]
	fn placeholder_refuses_to_download() {
		let pins = parse(r#"{"version": "0.0.0", "assets": {}}"#).unwrap();
		let err = pins.asset(&linux()).unwrap_err();
		assert!(err.contains("pins no datamitsu release"), "{err}");
	}

	#[test]
	fn resolves_the_asset_and_its_url() {
		let json = format!(
			r#"{{"version": "0.4.0", "assets": {{"linux_amd64": {{"file": "datamitsu_0.4.0_linux_amd64.tar.gz", "sha256": "{DIGEST}"}}}}}}"#
		);
		let pins = parse(&json).unwrap();
		let asset = pins.asset(&linux()).unwrap();
		assert_eq!(asset.sha256, DIGEST);
		assert_eq!(
			pins.url(asset),
			"https://github.com/datamitsu/datamitsu/releases/download/v0.4.0/datamitsu_0.4.0_linux_amd64.tar.gz"
		);
	}

	#[test]
	fn refuses_a_missing_target() {
		let pins = parse(r#"{"version": "0.4.0", "assets": {}}"#).unwrap();
		let err = pins.asset(&linux()).unwrap_err();
		assert!(err.contains("linux_amd64"), "{err}");
	}

	#[test]
	fn refuses_an_empty_or_malformed_hash() {
		for sha256 in ["", "abc", &"z".repeat(64)] {
			let json =
				format!(r#"{{"version": "0.4.0", "assets": {{"linux_amd64": {{"file": "a.tar.gz", "sha256": "{sha256}"}}}}}}"#);
			let pins = parse(&json).unwrap();
			assert!(pins.asset(&linux()).is_err(), "accepted sha256 {sha256:?}");
		}
	}
}
