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

Nothing regenerates it automatically: `task build:parsers` builds into Cargo's
target directory and regenerates the catalogue page, and copies nothing here. After
a change under `parsers/`, rebuild and copy it by hand, in the same change:

```bash
pnpm dm exec task -- build:parsers
cp parsers/target/wasm32-unknown-unknown/release/datamitsu_parsers.wasm internal/parsermanager/testdata/echo.wasm
```

## `released/`

Modules as they were published, which the core must keep reading. They are never
rebuilt or replaced; see [`released/README.md`](released/README.md).
