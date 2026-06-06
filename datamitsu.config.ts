/// <reference path="./.datamitsu/datamitsu.config.d.ts" />

const getBeforeConfigs = () => {
  return [{ path: "./node_modules/@shibanet0/datamitsu-config/datamitsu.config.js" }];
};
globalThis.getBeforeConfigs = getBeforeConfigs;

const getConfig = (config: config.Config) => {
  return config;
};
globalThis.getConfig = getConfig;

const getMinVersion = () => "0.0.0";

globalThis.getMinVersion = getMinVersion;
