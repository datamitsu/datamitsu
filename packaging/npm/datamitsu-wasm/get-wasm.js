import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Return the on-disk path to the bundled datamitsu parser WASM module. The
// single `.wasm` dispatches every parser by name; callers load it into their
// own WASM runtime (the Go core uses wazero).
export function getWasmPath() {
  const here = dirname(fileURLToPath(import.meta.url));
  const wasmPath = join(here, "datamitsu_parsers.wasm");

  if (existsSync(wasmPath)) {
    return wasmPath;
  }

  throw new Error(
    `datamitsu parser WASM not found at ${wasmPath}.\n` +
      `Please reinstall the package "@datamitsu/datamitsu-wasm".`,
  );
}
