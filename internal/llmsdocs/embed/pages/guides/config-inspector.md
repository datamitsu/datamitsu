# Config Inspector

> Open the resolved configuration for your repository, inspect a tool's scope and file patterns,

then export the same view as a single HTML artifact.

Open the resolved configuration for your repository, inspect a tool's scope and file patterns,
then export the same view as a single HTML artifact.

## Open this repository

```bash
datamitsu inspect
```

Open the printed URL. Universe is the default view. The server listens on loopback port 7744;
press Ctrl+C to stop it. It serves a snapshot, so restart after changing the config.
Use `datamitsu inspect --port 0` to let the OS choose a free port.

Normal config flags apply: `--config`, `--before-config`, and `--no-auto-config`.
The command resolves config through the same loader as `config show`; it does not run tools.

## Find out why a tool did not run

Search Operations for the tool. Inspect its enabled/skipped status, configured skip reason,
project types, operation scope, priority, and include/exclude patterns.
The Metadata tab exposes the exported tool definition.

**Status here is the config's declared status, not the result of an execution plan.** A tool
can be enabled in the inspector but skipped for a particular file selection or platform.
Ask the planner about that exact selection:

```bash
datamitsu lint package.json --explain
```

Use `--explain=json` for machine-readable planned tasks and skips. See
[narrowed runs](../reference/cli-commands.md#narrowed-runs) for whole-repository verdicts
and the widening policy.

## Compare config layers

The inspector shows the merged result. It does **not** record which layer introduced,
overrode, or removed a tool, and it does not expose artifact hashes. To inspect a base
in isolation, launch it with `--no-auto-config --config` pointing to that config file,
then compare with the normal repository snapshot. For the ordered inputs and override rules,
see [configuration chaining](configuration.md).

## Export and hand it to someone else

```bash
datamitsu inspect --output atlas.html
```

Open the file directly, upload it as a CI artifact, or publish it on any static host, including
in a subdirectory. It contains all scripts, styles, images, and display metadata. There are no
external asset or data requests. Node.js is needed only to build the interface during development.

To write to stdout:

```bash
datamitsu inspect --output - > atlas.html
```

`--output` and `--port` are mutually exclusive. Export replaces an existing file; its parent
directory must already exist.

The snapshot contains the configuration's own name; app names, descriptions, versions, runtime
families, dependencies and links (`officialUrl`, with `officialUrlDerived` saying whether the
configuration chose the page or datamitsu worked it out — see
[officialUrl](../reference/configuration-api.md#where-to-read-about-an-app-officialurl));
project markers; tool status and reasons; operation scopes, priorities and patterns; and
managed-file ownership. It omits environment values, arguments, file contents, hashes,
lockfiles, and download URLs. Review descriptions and other included text before publishing.
**Dataset** downloads it as JSON — the **inspector manifest**,
`datamitsu-inspector-manifest.json`, not executable config. The artifact carries the same document
in its `inspector-manifest` script element, and the showcase reads it to draw a configuration's
runtime fingerprint.

## Explore the views

- **Universe:** the starting view, with an app directory and an illustrative orbit. Colors show
  runtime membership; positions do not encode dependency relationships. Drag, zoom, or change layouts.
  A selected app links out where its configuration points: **Documentation** when the configuration
  named a page, **Package** or **Repository** when datamitsu derived one.
- **Operations:** filter tools, switch table/cards, and inspect a definition.
- **Blueprints:** configuration totals and runtime distribution, downloadable as SVG.

The search box, the project type and the runtime strip are one selection, and every view reads it:
pick `python-package` and the counters, the orbit, the tables and the diagrams all narrow to it, and
**Reset** returns all of them to the whole snapshot. In Universe the apps left out are dimmed rather
than removed — the sphere keeps its shape, so you read "39 of 111 lit", and a dimmed point answers
neither hover nor click. Operations and Blueprints drop what does not match instead, because a table
row or a bar has to be exactly what it claims. A shared link carries all three filters, so whoever
opens it sees the same selection in every view.

Quick find opens with **Cmd+K**, **Ctrl+K**, or **/**; arrows select and Enter opens a result.
On phones and tablets, selecting an item opens a modal detail panel; Escape or Close returns
to the list. The Auto theme follows the browser's color preference; Light and Dark override it.
Reduced motion stops automatic rotation. The Auto rotation control can also pause it.

This example is the reference wrapper `@shibanet0/datamitsu-config`, version
`0.0.0-unstable.20260912.f201136`, captured on 2026-09-22: 111 apps, 61 tool definitions,
14 project types and 59 managed-file definitions. Native binary: 75; Node.js: 20;
Python: 11; JVM: 3; Go: 1; Shell: 1. These are one maintainer's choices, not defaults.

<ConfigEmbed view="universe" />

[Open the full atlas](https://datamitsu.com/atlas.html). Environment-dependent settings in this
snapshot may differ from your repository or CI.

## Share or embed your own snapshot

Publish your HTML first. **Share view** copies a URL; **Embed** opens a section picker: Universe
(full section or the minimal orbit), runtime distribution, or tool definitions.
**Copy HTML** copies just that section as an iframe and nothing else: numbers printed beside the
frame would be a second copy of what the frame shows, and would disagree with it the first time
the configuration changes. Local-file and loopback URLs only work
on the same machine. Clipboard access can be unavailable; a dialog then lets you copy manually.
Label your embed with the config owner, version and capture date.

The query contract is the same for hosted and exported files:

| Parameter     | Values / behavior                                                                     |
| ------------- | ------------------------------------------------------------------------------------- |
| `view`        | `universe` (default), `operations`, `blueprints`                                      |
| `embed`       | `universe`, `runtimes`, or `operations` selects only that section; `1` follows `view` |
| `preset`      | `minimal`, with `embed=universe`: the orbit and its runtime strip only                |
| `theme`       | `auto` (default), `light`, `dark`                                                     |
| `tool`        | A tool ID in Operations; opens its detail, or limits an Operations embed to that tool |
| `runtime`     | Runtime family, for example `binary`, `node`, `python`, `jvm`, `go`, `shell`, `bun`   |
| `projectType` | A project-type ID declared in the snapshot                                            |

Unknown values fall back to defaults or no selection. Additional query parameters preserve
search (`q`), status, operation, list style, layout, selected app, detail section and diagram.

Hash bookmarks such as `atlas.html#/operations?tool=eslint&section=metadata` still work.
Outside embed mode, a `#/…` hash takes precedence over query view/filter values; `theme` and `embed` are always
read from the query. Embed mode ignores hashes so an old bookmark cannot change the section. In-page navigation uses hashes and supports back/forward without server
route rewrites. Share emits the current state entirely in the query, without a conflicting hash.

### Frame and sizing

```html
<iframe
  src="atlas.html?embed=runtimes&amp;theme=auto"
  loading="lazy"
  sandbox="allow-scripts"
  referrerpolicy="no-referrer"
  title="My configuration — runtime distribution"
  style="width:100%;aspect-ratio:6 / 1;min-height:180px;border:0;color-scheme:inherit"
></iframe>
```

Blueprints embeds are a single horizontal runtime strip. Reserve **6:1 with a 180px minimum
height**. Operations and Universe use **4:3 with a 420px minimum height**. The minimums keep
labels readable on phones; long tool lists scroll inside the frame. No messaging or parent
script is required. Universe embeds start still, support dragging, and have an accessible directory.

### The minimal orbit preset

`?embed=universe&preset=minimal` is the preset for a README or a landing page: the orbit and its
runtime strip, without the headline, the statistics, the search, the project-type filter, the view
switcher, the layout and zoom controls, or the app inspector panel.

```html
<iframe
  src="atlas.html?embed=universe&amp;preset=minimal&amp;theme=auto"
  loading="lazy"
  sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"
  referrerpolicy="no-referrer"
  title="My configuration — app orbit"
  style="width:100%;height:clamp(430px, 74svh, 780px);border:0;color-scheme:inherit"
></iframe>
```

- Hovering a point names its app, runtime and pinned version. Focusing a point label does the same.
- Selecting a point or a label opens that app in the full inspector, in a new tab.
- Selecting a runtime in the strip lights up its apps and dims the rest.
- The orbit rotates slowly and can be dragged. `prefers-reduced-motion: reduce` stops the rotation.
- Below the orbit: the runtime strip, the configuration's name and a link to the full inspector.
  The app directory is there for a screen reader and the keyboard, without taking visible room.

The sphere is fitted to the frame, so any height works and none of it is cut off. The extra
sandbox tokens let the frame open the full inspector in a new tab; with `allow-scripts` alone
selecting a point does nothing — an embed never navigates itself to the full site. The canvas takes the pointer, so on phones offer a poster image
with a control that loads the frame rather than letting a drag fight the page scroll. Its runtime
strip sits on a 1560px content rail with the documentation site's gutters, so a full-bleed frame
lines the strip up with the text above it while the orbit keeps the whole width.

### Following a host page's theme toggle

`theme=auto` follows the operating system, which is the wrong answer on a site with its own light
and dark switch. Post the mode to the frame instead:

```js
frame.contentWindow.postMessage({ type: "datamitsu:theme", value: "light" }, "*");
```

`value` is `light`, `dark` or `auto`. The frame applies it and answers its sender with
`{ type: "datamitsu:theme-ack", value }`. Treat a missing acknowledgement as an artifact too old to
know the message and reload the frame with `theme=` in the URL instead. The message only chooses
colors, and a sandboxed frame has no origin of its own to check yours against, so any origin may
send it — never send anything else through it.

The iframe's `color-scheme` inherits from its parent. Set `color-scheme: light` or
`color-scheme: dark` on the embedding container when your site has a manual theme; otherwise
use `color-scheme: light dark` to follow the OS. `theme=auto` reacts to that preference inside
the frame. No browser storage is needed.

**Write your own text beside the frame where a frame may not reach the reader.** A README
renderer may strip iframes entirely, a CSP may block one, and Markdown readers see neither;
a sentence of your own and a link to the hosted atlas cover all three. Add source, version and
capture date in plain Markdown as well. The snippet no longer generates that text for you: a
generated copy of the numbers goes stale the moment the configuration changes, while a sentence
you wrote stays yours to update.

The HTML must be reachable by your readers. A cross-origin host must permit framing (check
its `frame-ancestors` and `X-Frame-Options` headers). Your site's CSP needs the host in
`frame-src`, or `'self'` for same-origin hosting. The exported artifact contains inline scripts
and styles; a host CSP must permit them, for example with hashes computed from the final file.
The recommended sandbox grants scripts only; it grants neither same-origin access nor parent
navigation. Embeds do not fetch data, fonts, or assets.

## Restyle it with a theme file

Every color the inspector draws is a token. A theme file overrides the ones you name and keeps the
rest:

```json
{
  "$schema": "https://datamitsu.com/schemas/inspector-theme.schema.json",
  "dark": {
    "surface": { "base": "#151009" },
    "accent": { "base": "#e2a63e" },
    "runtime": { "node": "#7fd1b9" }
  },
  "light": { "surface": { "base": "#f7f3ea" } }
}
```

```bash
datamitsu inspect --theme ./inspector-theme.json
datamitsu inspect --theme ./inspector-theme.json --output atlas.html
datamitsu inspect --print-theme > inspector-theme.json
```

`--print-theme` writes the theme that would be applied — with no `--theme`, that is the complete
built-in one, which is the starting point for an override. Serving applies the merged theme; an
export bakes it into the HTML, so the file carries its colors wherever it is hosted.

| Group     | Tokens                                                             | Where they show                                                                                                                 |
| --------- | ------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------- |
| `surface` | `base`, `panel`, `raised`, `scrim`, `shadow`                       | Page, panels, controls, modal backdrops, shadows                                                                                |
| `text`    | `base`, `muted`                                                    | Body text and secondary text                                                                                                    |
| `line`    | `base`, `orbit`                                                    | Borders, table rules and dividers; `orbit` draws the sphere's rings and the ring around its center, and carries its own opacity |
| `accent`  | `base`, `display`, `glow`                                          | Links and selected states; `display` is the accent at heading size, and `glow` the orbit's rings and core                       |
| `focus`   | `ring`                                                             | The keyboard focus outline                                                                                                      |
| `status`  | `enabled`, `skipped`                                               | Tool status text and the status meter                                                                                           |
| `runtime` | `binary`, `bun`, `go`, `jvm`, `node`, `python`, `shell`, `unknown` | Runtime membership everywhere it is shown                                                                                       |

Both modes and every group and token are optional; `{}` is exactly the built-in theme. A `dark`
override never affects `light`. Colors are `#rgb`, `#rrggbb` or `#rrggbbaa`. Anything else — an
unknown mode, group or token, or a value that is not a color — is an error naming the full key
path, such as `dark.runtme.node: unknown key`, so a typo fails loudly instead of quietly doing
nothing. After merging, the command warns on stderr when text or accent colors fall below the
WCAG AA contrast minimum against their surface — 4.5:1 for body text, and 3:1 for `accent.display`,
which is only ever used at heading sizes; the theme still applies, because that is your call.

Changing the runtime colors breaks the correspondence with this site's orbit and strip, where a
reader has learned which color means which runtime. That is a trade worth making deliberately.

## Publish once, update from the config repository

An embed can point directly at the HTML hosted by your config repository. Generate it after
updating the config, and publish it at the same URL on each deployment:

```bash
datamitsu inspect --output atlas.html
```

Use that published URL in the iframe `src`, followed by `?embed=universe`,
`?embed=runtimes`, or `?embed=operations&tool=eslint`. Each mode renders a dedicated content
section, without the surrounding inspector. The documentation site does not need to rebuild
when you replace the artifact. Readers see the new snapshot on their next page load, subject
to the host's cache policy; configure HTML to revalidate instead of caching it as immutable.
An already-open frame does not poll for changes.

The website's `ConfigEmbed` component also accepts `src` for an external artifact. With an
external source it links to that artifact rather than showing counts or a capture date from
the bundled example. Leave `src` unset for the local demonstration. Publishing a wrapper's
HTML does not make that wrapper mandatory: datamitsu accepts your own configuration too.
