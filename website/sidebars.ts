import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  docsSidebar: [
    "intro",
    "about",
    {
      items: [
        {
          items: [
            "getting-started/installation/homebrew",
            "getting-started/installation/winget",
            "getting-started/installation/scoop",
            "getting-started/installation/npm",
            "getting-started/installation/pypi",
            "getting-started/installation/rubygems",
            "getting-started/installation/docker",
            "getting-started/installation/github-releases",
            "getting-started/installation/source",
            "getting-started/installation/vscode",
          ],
          label: "Installation",
          link: { id: "getting-started/installation/index", type: "doc" },
          type: "category",
        },
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
        "guides/supply-chain-security",
        "guides/oci-bundles",
        {
          items: [
            "guides/architecture/planner",
            "guides/architecture/execution",
            "guides/architecture/discovery",
            "guides/architecture/caching",
            "guides/architecture/parsers",
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
        "how-to/use-in-github-actions",
      ],
      label: "How-To",
      link: { type: "generated-index" },
      type: "category",
    },
    {
      items: [
        "reference/cli-commands",
        "reference/configuration-api",
        "reference/parser-catalog",
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
