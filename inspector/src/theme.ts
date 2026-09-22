/**
 * The color tokens live in internal/inspector/theme.json, which Go reads to bake a theme into an
 * export. A page that assembles the template itself — the dev server, the documentation site's
 * atlas — renders the same file with this, so no palette is ever written down twice.
 */
export type ThemeFile = Record<string, Record<string, Record<string, string>> | string>;

export function themeCSS(theme: ThemeFile): string {
  return `:root{${palette(theme, "light")}}:root[data-theme="dark"]{${palette(theme, "dark")}}`;
}

function palette(theme: ThemeFile, mode: string): string {
  const groups = theme[mode];
  if (!groups || typeof groups === "string") {
    return "";
  }
  return Object.entries(groups)
    .flatMap(([group, tokens]) =>
      Object.entries(tokens).map(([token, value]) => `--${group}-${token}:${value};`),
    )
    .join("");
}
