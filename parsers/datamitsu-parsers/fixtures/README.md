# Recorded tool output

Every parser the reference configuration wires has two recordings here, taken
from the real tool rather than written by hand:

- `clean.*` — a run over input the tool accepts: exit 0 and no finding. The
  parser must return no diagnostic.
- `findings.*` — a run over input with findings. `src/tools/fixtures.rs` asserts
  the parser's diagnostics field by field.

Each recording is three files: `.stdout` and `.stderr` hold the tool's streams
byte for byte, `.exit` its exit code. Absolute paths the tools printed are
rewritten to `/work/<tool>` and `/home/user`; nothing else is edited.

A parser that answers every output with nothing passes a clean-output test and
fails its findings test. A tool release that changes the output format is caught
when its recording is taken again, which is why a tool bump in a configuration
re-records the tool's pair here and runs `cargo test`.

These are test data. They are not compiled into the module: its bytes do not
change when a recording does.

## Tools and arguments

Recorded with the versions the reference configuration pinned at the time, with
the arguments it passes (minus cache paths, and `--no-config` for golangci-lint
so a repository config cannot enable other linters):

| Parser          | Tool and version      | Arguments                                                            |
| --------------- | --------------------- | -------------------------------------------------------------------- |
| `actionlint`    | actionlint v1.7.12    | `-no-color -format '{{json .}}' <workflow>`                          |
| `checkmake`     | checkmake v0.3.2      | `--format={{.LineNumber}}:{{.Rule}}:{{.Violation}}\n <makefile>`     |
| `cspell`        | cspell 10.0.1         | `lint -c cspell.json --quiet --no-must-find-files --unique <file>`   |
| `dclint`        | dclint 3.1.0          | `--formatter json <compose file>`                                    |
| `dotenv_linter` | dotenv-linter v4.0.0  | `check <file>`                                                       |
| `eslint`        | eslint 10.9.0         | `--quiet --format=json -c eslint.config.mjs <file>`                  |
| `golangci_lint` | golangci-lint v2.13.1 | `run --no-config --allow-parallel-runners --output.json.path=stdout` |
| `hadolint`      | hadolint v2.15.1      | `-c hadolint.yaml --format=json <dockerfile>`                        |
| `harper_cli`    | harper-cli v2.8.0     | `lint --dialect us --format compact <file>`                          |
| `protolint`     | protolint v0.57.0     | `lint --reporter json <file>`                                        |
| `tsc`           | TypeScript 7.0.2      | `--noEmit` in a directory with a strict `tsconfig.json`              |
| `vale`          | vale v3.18.0          | `--config .vale.ini --output JSON <file>` (`BasedOnStyles = Vale`)   |
| `yamllint`      | yamllint 1.38.0       | `-c .yamllint.yaml --strict -f parsable <file>` (`extends: default`) |

To record a pair again, run the tool through the configuration's own app
(`datamitsu exec <app> -- <arguments>`) over a clean input and over one with a
few findings, write stdout, stderr and the exit code into the three files, and
update the expected diagnostics in `src/tools/fixtures.rs`. A findings recording
that parses to nothing fails the test, whatever the expectations say.
