# Parser module fixtures

## `echo.wasm`

A build of the current crate, `parsers/datamitsu-parsers`, with every parser it
carries. Despite the name it is not an echo-only module: `echo` is one of its
dispatch keys. It is the module the core's current contract is tested against —
`runtime_test.go`, the pool tests, `internal/runner/parser_test.go` (served over
`httptest`), the `devtools parsers` tests of `cmd` and `test/cli`, and the
execution scenarios of `test/cli`, which seed it into an offline store
(`clitest.SeedParserModule`).

It is built without `DATAMITSU_PARSERS_VERSION`, so `describe` reports the crate
version rather than a release version.

After a change under `parsers/`, rebuild it in the same change:

```bash
pnpm dm exec task -- build:parsers:fixture
```

The task builds the public module with the toolchain `parsers/rust-toolchain.toml`
pins, copies it here and regenerates the catalogue page. It remaps the cargo home,
the toolchain's source tree and the workspace to fixed names, so the committed
bytes name no directory of the machine that built them. `task build:parsers` alone
builds into Cargo's target directory and copies nothing here.

## `released/`

Modules as they were published, which the core must keep reading. They are never
rebuilt or replaced; see [`released/README.md`](released/README.md).
