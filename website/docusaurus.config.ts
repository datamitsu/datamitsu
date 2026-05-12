import type * as Preset from "@docusaurus/preset-classic";
import type { Config } from "@docusaurus/types";

import { withOvinekoDocs } from "@ovineko/docusaurus-config";
import { themes as prismThemes } from "prism-react-renderer";

const config: Config = {
  baseUrl: "/",
  deploymentBranch: "gh-pages",
  favicon: "img/favicon.ico",

  i18n: {
    defaultLocale: "en",
    locales: ["en"],
  },

  markdown: {
    hooks: {
      onBrokenMarkdownLinks: "throw",
    },
    mermaid: true,
  },
  onBrokenAnchors: "throw",
  onBrokenLinks: "throw",
  organizationName: "datamitsu",

  presets: [
    [
      "classic",
      {
        blog: {
          editUrl: "https://github.com/datamitsu/datamitsu/tree/main/website/",
          showReadingTime: true,
        },
        docs: {
          editUrl: "https://github.com/datamitsu/datamitsu/tree/main/website/",
          sidebarPath: "./sidebars.ts",
        },
        theme: {
          customCss: "./src/css/custom.css",
        },
      } satisfies Preset.Options,
    ],
  ],
  projectName: "datamitsu",
  scripts: [],

  tagline: "Your toolchain deserves a home.",

  themeConfig: {
    algolia: {
      // Public API key: it is safe to commit it
      apiKey: "074677e081f2ba8b978c240e37a3ce59", // pragma: allowlist secret // trufflehog:ignore
      appId: "252YCJ4OET",
      indexName: "datamitsu documentation website",
    },

    asciinema: {
      themes: {
        dark: "monokai", // cspell:disable-line
        light: "solarized-light",
      },
    },
    colorMode: {
      respectPrefersColorScheme: true,
    },
    footer: {
      copyright: `Copyright \u00A9 ${new Date().getFullYear()} datamitsu<br/>Your toolchain deserves a home.`,
      links: [
        {
          items: [
            {
              label: "Getting Started",
              to: "/docs/intro",
            },
          ],
          title: "Docs",
        },
        {
          items: [
            {
              href: "https://github.com/datamitsu/datamitsu",
              label: "GitHub",
            },
            {
              href: "https://npmx.dev/package/@datamitsu/datamitsu",
              label: "npmx.dev",
            },
            {
              href: "https://www.npmjs.com/package/@datamitsu/datamitsu",
              label: "npm",
            },
          ],
          title: "Community",
        },
      ],
    },
    image: "img/opengraph.png",
    mermaid: {
      theme: { dark: "dark", light: "neutral" },
    },
    navbar: {
      items: [
        {
          label: "Docs",
          position: "left",
          sidebarId: "docsSidebar",
          type: "docSidebar",
        },
        {
          label: "Blog",
          position: "left",
          to: "/blog",
        },
        {
          href: "https://github.com/datamitsu/datamitsu",
          label: "GitHub",
          position: "right",
        },
      ],
      logo: {
        alt: "datamitsu Logo",
        src: "img/icon.png",
      },
      title: "datamitsu",
    },
    prism: {
      additionalLanguages: ["bash", "json", "toml", "yaml"],
      darkTheme: prismThemes.gruvboxMaterialDark,
      theme: prismThemes.vsLight,
    },
  } satisfies Preset.ThemeConfig,

  themes: ["@docusaurus/theme-mermaid"],
  title: "Datamitsu",

  url: "https://datamitsu.com",
};

export default withOvinekoDocs(config, {
  idealImage: {
    disableInDev: false,
    max: 1030,
    min: 640,
    quality: 70,
    steps: 2,
  },
  umami: {
    src: "https://a.shibanet0.com/pzjlkgj6ujcurpo",
    websiteId: "85a400b1-e4d4-4eb8-bdd9-cc95fc51bdef",
  },
});
