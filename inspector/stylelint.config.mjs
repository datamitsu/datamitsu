import { defineConfig } from "../.datamitsu/stylelint.config.mjs";

export default defineConfig({
  ignoreFiles: ["index.html"],
  rules: {
    "no-descending-specificity": null,
  },
});
