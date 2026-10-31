use std::collections::BTreeMap;

use serde::Deserialize;
use zed_extension_api::serde_json;

use crate::digest;
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
#[serde(deny_unknown_fields)]
pub struct Asset {
	pub file: String,
	pub hash: String,
}

pub fn pinned() -> Result<Pins, String> {
	parse(include_str!("../release.json"))
}

pub fn parse(json: &str) -> Result<Pins, String> {
	let pins: Pins = serde_json::from_str(json).map_err(|err| format!("release.json is malformed: {err}"))?;
	for asset in pins.assets.values() {
		digest::validate_sha256(&asset.hash).map_err(|err| format!("the pinned hash of {}: {err}", asset.file))?;
	}
	Ok(pins)
}

impl Pins {
	/// Refuses placeholder builds and targets without a verified archive pin.
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
		Ok(asset)
	}

	pub fn url(&self, asset: &Asset) -> String {
		format!(
			"https://github.com/{REPOSITORY}/releases/download/v{}/{}",
			self.version, asset.file
		)
	}
}

#[cfg(test)]
mod tests {
	use zed_extension_api::{Architecture, Os};

	use super::*;

	const DIGEST: &str = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";

	fn linux() -> Target {
		Target::from_platform(Os::Linux, Architecture::X8664).unwrap()
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
			r#"{{"version": "0.4.0", "assets": {{"linux_amd64": {{"file": "datamitsu_0.4.0_linux_amd64.tar.gz", "hash": "{DIGEST}"}}}}}}"#
		);
		let pins = parse(&json).unwrap();
		let asset = pins.asset(&linux()).unwrap();
		assert_eq!(asset.hash, DIGEST);
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
	fn refuses_invalid_hashes_when_loading_pins() {
		for hash in [
			"".to_string(),
			"abc".to_string(),
			"a".repeat(64),
			format!("sha256:{}", "A".repeat(64)),
			format!("sha256:{}", "z".repeat(64)),
			format!("xxh3:{}", "a".repeat(32)),
		] {
			let json =
				format!(r#"{{"version": "0.4.0", "assets": {{"linux_amd64": {{"file": "a.tar.gz", "hash": "{hash}"}}}}}}"#);
			let err = parse(&json).unwrap_err();
			assert!(err.contains("sha256:<64 lowercase hex>"), "{err}");
		}
	}

	#[test]
	fn refuses_a_missing_hash_and_the_obsolete_sha256_field() {
		for asset in [
			r#"{"file":"a.tar.gz"}"#.to_string(),
			format!(r#"{{"file":"a.tar.gz","sha256":"{DIGEST}"}}"#),
		] {
			let json = format!(r#"{{"version":"0.4.0","assets":{{"linux_amd64":{asset}}}}}"#);
			assert!(parse(&json).is_err(), "accepted {json}");
		}
	}
}
