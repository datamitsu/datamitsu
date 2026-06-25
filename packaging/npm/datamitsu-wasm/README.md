# @datamitsu/datamitsu-wasm

Platform-independent npm delivery of datamitsu's signed Rust→WASM parser modules.

A single `datamitsu_parsers.wasm` dispatches every parser by name. This package
bundles that module and exposes its on-disk path:

```js
import { getWasmPath } from "@datamitsu/datamitsu-wasm/get-wasm.js";

const wasmPath = getWasmPath(); // absolute path to datamitsu_parsers.wasm
```

The module's integrity is covered by the release's signed `checksums.txt`. The
authoritative `{ name → { url, hash, version } }` mapping config maintainers
import lives in the release **parser manifest** (`parser-manifest.json`), whose
hashes equal the `checksums.txt` entries.

This package is built and published by `packaging/pack.ts`; the `.wasm` itself is
copied in at release time and is not committed.
