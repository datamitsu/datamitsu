import { defineConfig } from "./.datamitsu/knip.config.js";

export default defineConfig((config) => ({ ...config, ignoreBinaries: ["go"] }));
