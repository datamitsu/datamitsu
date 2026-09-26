# Released parser modules

Each file here is a parser module as it was published: the bytes a configuration
pinned, fetched by digest and verified against the SHA-256 it declared. They hold
the core to the modules users can still be running. `released_test.go` reads them
offline and asserts what they report, recorded literally in the test.

**The files are immutable.** A fixture is not replaced when the wrapper's pin moves
to a newer module; it stays as the contract the core must keep reading. A later
response ABI gets its own directory (`v2/…`) once a module of that ABI has been
released. The file is named by the prefix of its SHA-256, because a configuration
pins a module by hash, not by a release name.

## `v1/b5425355.wasm`

The last released module of response ABI v1 (`parse` returns a JSON array of
diagnostics whose fields other than `message` are nullable) and descriptor schema 1.

| Property            | Value                                                                                                                                                                                                                  |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| SHA-256             | `b5425355969f69f9a80a9838269b350b33ab47ec290358d7aa761483a2921986`                                                                                                                                                     |
| Size                | 385160 bytes                                                                                                                                                                                                           |
| Registry            | `ghcr.io/datamitsu/datamitsu-parsers`, manifest `sha256:33f5437bfa8c974e79f5d597af2009a56f2b438792958d62ee885749b445d5e2`, single layer `sha256:b5425355…` (`application/wasm`, titled `datamitsu_parsers_0.2.1.wasm`) |
| Manifest annotation | `com.datamitsu.parsers-version: 0.2.1`, `org.opencontainers.image.revision: eab36531114cac6aa56f03608c5b239470f19f6e`                                                                                                  |
| Release asset       | `datamitsu_parsers_0.2.1.wasm` of the `v0.2.1` release of `datamitsu/datamitsu`, whose `checksums.txt` lists the same SHA-256                                                                                          |
| `describe`          | module `datamitsu-parsers`, version `v0.2.1`, `schemaVersion: 1`, 93 tools, exports `reset`                                                                                                                            |
| Pinned by           | `@shibanet0/datamitsu-config` `0.0.0-unstable.20260912.f201136`, entry `parsers.core` (OCI source by manifest digest)                                                                                                  |
| Fetched             | 2026-09-27, by the manifest digest above; the layer blob was verified against the SHA-256, and the release checksum agrees                                                                                             |

To fetch it again and compare, pull the layer by digest with an anonymous token:

```bash
token=$(curl -s "https://ghcr.io/token?scope=repository:datamitsu/datamitsu-parsers:pull" | jq -r .token)
curl -sL -H "Authorization: Bearer $token" -o module.wasm \
  https://ghcr.io/v2/datamitsu/datamitsu-parsers/blobs/sha256:b5425355969f69f9a80a9838269b350b33ab47ec290358d7aa761483a2921986
sha256sum module.wasm
```
