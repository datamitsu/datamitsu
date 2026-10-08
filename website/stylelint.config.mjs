import { defineConfig } from "../.datamitsu/stylelint.config.mjs";

export default defineConfig({
  rules: {
    "no-descending-specificity": null,
    "no-duplicate-selectors": null,
    "property-no-unknown": [true, { ignoreProperties: ["composes"] }],
    "selector-class-pattern": null,
    "selector-pseudo-class-no-unknown": [true, { ignorePseudoClasses: ["global"] }],
  },
});
