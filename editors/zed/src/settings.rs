use std::collections::HashMap;

use zed_extension_api::serde_json::{Value, json};

/// The server's environment fallback for `format.timeoutMs`.
const FORMAT_TIMEOUT_ENV: &str = "DATAMITSU_LSP_FORMAT_TIMEOUT_MS";

/// Zed stops waiting for a format after five seconds (`FORMAT_TIMEOUT` in its
/// editor crate, not configurable) and saves the buffer while the tools may
/// still be writing the file. The watchdog keeps a further group of tools from
/// starting that late; the server's own default, 15 s, is past Zed's limit.
const DEFAULT_FORMAT_TIMEOUT_MS: u64 = 3000;

pub fn default_arguments() -> Vec<String> {
	vec!["lsp".to_string()]
}

/// The initialization options the extension contributes. Zed merges the user's
/// `lsp.datamitsu.initialization_options` over them, so a `format.timeoutMs` the
/// user sets wins; one set in the server's environment wins too, because an
/// initialization option would override it.
pub fn initialization_options<'a>(env_keys: impl IntoIterator<Item = &'a str>) -> Option<Value> {
	if env_keys.into_iter().any(|key| key == FORMAT_TIMEOUT_ENV) {
		return None;
	}
	Some(json!({ "format": { "timeoutMs": DEFAULT_FORMAT_TIMEOUT_MS } }))
}

/// The shell environment with the user's `binary.env` laid over it.
pub fn merge_env(mut env: Vec<(String, String)>, overrides: Option<HashMap<String, String>>) -> Vec<(String, String)> {
	let Some(overrides) = overrides else {
		return env;
	};
	env.retain(|(key, _)| !overrides.contains_key(key));
	let mut overrides: Vec<_> = overrides.into_iter().collect();
	overrides.sort();
	env.extend(overrides);
	env
}

#[cfg(test)]
mod tests {
	use super::*;

	#[test]
	fn defaults_the_format_watchdog_below_zeds_limit() {
		let options = initialization_options(["PATH", "HOME"]).unwrap();
		assert_eq!(options, json!({ "format": { "timeoutMs": 3000 } }));
	}

	#[test]
	fn leaves_the_watchdog_to_the_environment_when_it_sets_one() {
		assert_eq!(
			initialization_options(["PATH", "DATAMITSU_LSP_FORMAT_TIMEOUT_MS"]),
			None
		);
	}

	#[test]
	fn lays_binary_env_over_the_shell_env() {
		let shell = vec![
			("PATH".to_string(), "/usr/bin".to_string()),
			("HOME".to_string(), "/home/u".to_string()),
		];
		let overrides = HashMap::from([
			("PATH".to_string(), "/opt/bin".to_string()),
			("DATAMITSU_LOG_LEVEL".to_string(), "debug".to_string()),
		]);
		assert_eq!(
			merge_env(shell.clone(), Some(overrides)),
			[
				("HOME".to_string(), "/home/u".to_string()),
				("DATAMITSU_LOG_LEVEL".to_string(), "debug".to_string()),
				("PATH".to_string(), "/opt/bin".to_string()),
			]
		);
		assert_eq!(merge_env(shell.clone(), None), shell);
	}
}
