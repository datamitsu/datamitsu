import { config } from "./.datamitsu/knip.config.js";

const internalConfig = { ...config, ignoreBinaries: ["go"] };

export default internalConfig;
