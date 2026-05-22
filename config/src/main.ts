import { mapOfApps } from "./apps";
import { DATAMITSU_AGENT_GUIDE } from "./prompts/generated";
import { mapOfRuntimes } from "./runtimes";

const pnpmWorkspaceDefaults = {
  blockExoticSubdeps: true,
  dangerouslyAllowAllBuilds: false,
  enablePrePostScripts: false,
  lockfile: true,
  minimumReleaseAge: 10_080,
  preferFrozenLockfile: true,
  strictDepBuilds: true,
  trustPolicy: "no-downgrade",
};

function getConfig(config: config.Config): config.Config {
  /** @type config.Config */
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
