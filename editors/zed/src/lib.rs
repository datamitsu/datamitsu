mod archive;
mod digest;
mod install;
mod platform;
mod release;
mod settings;

use std::path::Path;

use zed_extension_api::serde_json::Value;
use zed_extension_api::settings::{CommandSettings, LspSettings};
use zed_extension_api::{self as zed, LanguageServerId, LanguageServerInstallationStatus, Result};

use crate::platform::Target;

const BINARY: &str = "datamitsu";

struct DatamitsuExtension {
	installed: Option<String>,
}

impl DatamitsuExtension {
	/// A datamitsu on the worktree's PATH, else the release this build pins.
	fn binary_path(&mut self, id: &LanguageServerId, worktree: &zed::Worktree) -> Result<String> {
		if let Some(path) = worktree.which(BINARY) {
			return Ok(path);
		}
		if let Some(path) = &self.installed
			&& Path::new(path).is_file()
		{
			return Ok(path.clone());
		}
		let path = install_pinned(id).inspect_err(|err| {
			zed::set_language_server_installation_status(id, &LanguageServerInstallationStatus::Failed(err.clone()));
		})?;
		self.installed = Some(path.clone());
		Ok(path)
	}
}

fn install_pinned(id: &LanguageServerId) -> Result<String> {
	let (os, arch) = zed::current_platform();
	let target = Target::from_platform(os, arch)?;
	let pins = release::pinned()?;
	install::install(Path::new("."), &pins, &target, &ZedHost { id })
}

fn binary_settings(id: &LanguageServerId, worktree: &zed::Worktree) -> Option<CommandSettings> {
	LspSettings::for_worktree(id.as_ref(), worktree)
		.ok()
		.and_then(|settings| settings.binary)
}

struct ZedHost<'a> {
	id: &'a LanguageServerId,
}

impl install::Host for ZedHost<'_> {
	fn download(&self, url: &str, path: &str) -> Result<(), String> {
		zed::download_file(url, path, zed::DownloadedFileType::Uncompressed).map_err(|err| format!("download {url}: {err}"))
	}

	fn make_executable(&self, path: &str) -> Result<(), String> {
		zed::make_file_executable(path)
	}

	fn downloading(&self) {
		zed::set_language_server_installation_status(self.id, &LanguageServerInstallationStatus::Downloading);
	}

	fn done(&self) {
		zed::set_language_server_installation_status(self.id, &LanguageServerInstallationStatus::None);
	}
}

impl zed::Extension for DatamitsuExtension {
	fn new() -> Self {
		Self { installed: None }
	}

	fn language_server_command(&mut self, id: &LanguageServerId, worktree: &zed::Worktree) -> Result<zed::Command> {
		let binary = binary_settings(id, worktree);
		let command = match binary.as_ref().and_then(|binary| binary.path.clone()) {
			Some(path) => path,
			None => self.binary_path(id, worktree)?,
		};
		let args = binary
			.as_ref()
			.and_then(|binary| binary.arguments.clone())
			.unwrap_or_else(settings::default_arguments);
		let env = settings::merge_env(worktree.shell_env(), binary.and_then(|binary| binary.env));
		Ok(zed::Command { command, args, env })
	}

	fn language_server_initialization_options(
		&mut self,
		id: &LanguageServerId,
		worktree: &zed::Worktree,
	) -> Result<Option<Value>> {
		let shell = worktree.shell_env();
		let binary_env = binary_settings(id, worktree)
			.and_then(|binary| binary.env)
			.unwrap_or_default();
		let keys = shell
			.iter()
			.map(|(key, _)| key.as_str())
			.chain(binary_env.keys().map(String::as_str));
		Ok(settings::initialization_options(keys))
	}
}

zed::register_extension!(DatamitsuExtension);
