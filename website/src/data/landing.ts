// Every command is copied as one line that runs when pasted into that channel's
// own shell: Homebrew taps as part of the install, and Scoop joins its two steps
// with `;` because Windows PowerShell 5.1 has no `&&` and no backslash newline.
export const installChannels = [
  { command: "brew install datamitsu/tap/datamitsu", id: "homebrew", label: "Homebrew" },
  { command: "winget install datamitsu.datamitsu", id: "winget", label: "Winget" },
  {
    command:
      "scoop bucket add datamitsu https://github.com/datamitsu/scoop-bucket.git; scoop install datamitsu",
    id: "scoop",
    label: "Scoop",
  },
  { command: "npm install -g @datamitsu/datamitsu", id: "npm", label: "npm" },
  { command: "uv tool install datamitsu", id: "pypi", label: "PyPI" },
  { command: "gem install datamitsu", id: "rubygems", label: "RubyGems" },
  {
    command: 'docker run --rm -v "$PWD:/workspace" datamitsu/datamitsu:latest lint',
    id: "docker",
    label: "Docker",
  },
  {
    command: "https://github.com/datamitsu/datamitsu/releases",
    id: "github-releases",
    label: "Releases",
  },
];
// The tools worth putting in front of a reader who is deciding whether this is
// for their stack: names they can place without looking them up. This list is the
// one editorial input into the runtime families row — which family a tool belongs
// to, and whether it is there at all, still comes from the configuration's own
// dataset. A name that is not in the dataset is simply not shown.
export const showcaseTools = [
  "cspell",
  "eslint",
  "gitleaks",
  "golangci-lint",
  "govulncheck",
  "hadolint",
  "ktfmt",
  "ktlint",
  "mypy",
  "openapi-generator",
  "oxlint",
  "prettier",
  "ruff",
  "semgrep",
  "shellcheck",
  "shfmt",
  "trivy",
  "typst",
  "yamllint",
];

// Seven files a repository grows on any stack: a reader coming from Go, Python,
// Rust or Typst has to see their own repository here, not a JavaScript one.
export const scatteredFiles = [
  { name: "README.md", note: "“first, install these fourteen things…”" },
  { name: "Makefile", note: "lint: / fmt: / check:" },
  { name: ".github/workflows/ci.yml", note: "setup-go · setup-uv · caches" },
  { name: ".pre-commit-config.yaml", note: "hook versions, again" },
  { name: ".tool-versions", note: "one runtime pin per language" },
  { name: "pyproject.toml", note: "linter versions mixed into dependencies" },
  { name: "scripts/setup.sh", note: "brew install ×14" },
];
