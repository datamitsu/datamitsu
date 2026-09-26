import { mapOfApps } from "./apps";
import { DATAMITSU_AGENT_GUIDE, DATAMITSU_CONFIG_AUTHOR_GUIDE } from "./prompts/generated";
import { mapOfRuntimes } from "./runtimes";

function getConfig(config: config.Config): config.Config {
  /**
   * @type config.Config
   */
  const configOutput = {
    ...config,
    apps: {
      ...config.apps,
      ...mapOfApps,
    },
    runtimes: {
      ...config.runtimes,
      ...mapOfRuntimes,
    },
    sharedStorage: {
      ...config.sharedStorage,
      "datamitsu-agent-prompt": DATAMITSU_AGENT_GUIDE,
      "datamitsu-config-author-prompt": DATAMITSU_CONFIG_AUTHOR_GUIDE,
      "pnpm-workspace-defaults": YAML.stringify(pnpmWorkspaceDefaults),
    },
  };

  return configOutput;
}

globalThis.getConfig = getConfig;

function getMinVersion(): string {
  return "0.0.1";
}

globalThis.getMinVersion = getMinVersion;
