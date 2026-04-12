import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  docsSidebar: [
    "intro",
    "about",
    {
      items: [
        "getting-started/installation",
        "getting-started/quick-start",
        "getting-started/core-concepts",
      ],
      label: "Getting Started",
      link: { type: "generated-index" },
      type: "category",
    },
    {
      items: [
        "guides/configuration",
        "guides/binary-management",
        "guides/runtime-management",
        "guides/managed-configs",
        "guides/managed-content",
        "guides/tooling-system",
        "guides/using-wrappers",
        {
          items: [
            "guides/architecture/planner",
            "guides/architecture/execution",
            "guides/architecture/discovery",
            "guides/architecture/caching",
          ],
          label: "Architecture",
          link: { id: "guides/architecture/index", type: "doc" },
          type: "category",
        },
      ],
      label: "Guides",
      link: { type: "generated-index" },
      type: "category",
    },
    {
      items: [
        "how-to/add-new-tool",
        "how-to/configure-linters",
        "how-to/use-remote-configs",
        "how-to/manage-cache",
        "how-to/maintain-wrapper",
        "how-to/use-in-alpine",
      ],
      label: "How-To",
      link: { type: "generated-index" },
      type: "category",
    },
    {
      items: [
        "reference/cli-commands",
        "reference/configuration-api",
        "reference/js-api",
        "reference/template-placeholders",
        "reference/ignore-rules",
        "reference/comparison",
      ],
      label: "Reference",
      link: { type: "generated-index" },
      type: "category",
    },
    {
      items: ["examples/multiple-versions", "examples/uv-isolation", "examples/pnpm-patterns"],
      label: "Examples",
      link: { type: "generated-index" },
      type: "category",
    },
    {
      items: [
        "contributing/index",
        "contributing/brand-guidelines",
        "contributing/creating-wrappers",
      ],
      label: "Contributing",
      link: { type: "generated-index" },
      type: "category",
    },
  ],
};

export default sidebars;
