<!-- TEMPLATE: __VERSION__ placeholders are substituted with the released
     datamitsu version by scripts/publish.ts when this README is copied into
     the datamitsu/setup-datamitsu dist repository. -->

# setup-datamitsu

GitHub Action that installs the [datamitsu](https://github.com/datamitsu/datamitsu)
CLI, provisions the tools your repository's config declares, and runs datamitsu
in CI — in a single step.

## Usage

```yaml
steps:
  - uses: actions/checkout@v4
  - uses: datamitsu/setup-datamitsu@v__VERSION__
```

That installs datamitsu `__VERSION__` and runs `datamitsu init` to provision the managed
tools your config declares. By default it then runs `datamitsu lint` (override
with `args`). The action carries no configuration of its own — it reads the
datamitsu config already in your repository.

Run a different command, or only set up the CLI:

```yaml
# Run an arbitrary datamitsu command instead of `lint`
- uses: datamitsu/setup-datamitsu@v__VERSION__
  with:
    args: "exec golangci-lint run ./..."

# Install + init only (put `datamitsu` on PATH, run nothing)
- uses: datamitsu/setup-datamitsu@v__VERSION__
  with:
    args: ""
```

## Versioning and integrity

Each release tag pins **one** datamitsu version: `@v__VERSION__` installs datamitsu
`__VERSION__`. The SHA-256 hashes of every release binary are baked into that tag
(inlined in the non-minified `dist/index.mjs`) and verified on download — an
unverified or missing hash makes the action fail.

Pin by full version (`@v__VERSION__`) or commit SHA, **not** a moving major tag, and
let [Dependabot](https://docs.github.com/code-security/dependabot) keep it
current:

```yaml
# .github/dependabot.yml
version: 2
updates:
  - package-ecosystem: "github-actions"
    directory: "/"
    schedule:
      interval: "weekly"
```

## Inputs

| Input  | Default | Description                                                                                 |
| ------ | ------- | ------------------------------------------------------------------------------------------- |
| `args` | `lint`  | Arguments passed to `datamitsu` after setup. An empty string installs and initializes only. |
| `init` | `true`  | Run `datamitsu init` after install to provision managed tools.                              |

## Outputs

| Output    | Description                               |
| --------- | ----------------------------------------- |
| `version` | The datamitsu version that was installed. |

## About this repository

This repository is **auto-generated** from
[`datamitsu/datamitsu`](https://github.com/datamitsu/datamitsu) (the
`packaging/action` package) on every datamitsu release. Do not edit it by hand.
Open issues and pull requests in the main repository.

Full documentation: <https://datamitsu.com/docs/how-to/use-in-github-actions>

## License

[MIT](LICENSE)
