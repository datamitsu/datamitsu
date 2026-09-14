---
worth: yes
where: internal/runtimemanager/hash.go:109
added: 2026-09-14
---

# Install-time app env is missing from install hashes

Changing an app's `env` can change its installed output, but core reuses the same
install path and accepts the old installation. `GetAppPath` in
`internal/runtimemanager/runtimemanager.go` receives versions, package dependencies,
runtime identity, lockfiles, files and archives; it never receives `app.Env`.
Both app hash functions in `hash.go` therefore omit an input the installer reads.

## Affected kinds

| Kind | Installer receives `app.Env` through                                     | Install hash                                 |
| ---- | ------------------------------------------------------------------------ | -------------------------------------------- |
| Bun  | `installBunApp` → `installPNPMAppOnce` → `mergeInstallEnv`               | `calculatePackageAppHash`                    |
| Node | `installNodeApp` → `installPNPMAppOnce` → `mergeInstallEnv`              | `calculatePackageAppHash`                    |
| UV   | `InstallUVApp` → `installUVAppOnce` → `mergeInstallEnv`                  | `calculateAppHash`                           |
| Go   | `InstallGoApp` → `installGoAppOnce` → `mergeInstallEnv`                  | `calculateAppHash`                           |
| JVM  | `InstallJVMApp` receives no app environment; it downloads a verified JAR | `calculateAppHash`; not affected by this gap |

`GetCommandInfo` passes `app.Env` to the four affected installers. The pnpm
installer in `pnpm.go`, UV's sync command in `uv.go`, and Go's build command in
`go.go` merge it into the child environment. Runtime-owned keys win, but other
keys still reach build and lifecycle steps. For example, changing a custom build
mode used by an allowed pnpm lifecycle script can leave the old compiled output
in place. UV does not use `calculatePackageAppHash`: that function is specific
to Node and Bun; its generic hash has the same omission.

## Shape of a fix

Thread install-time app environment through path computation and hash a stable,
sorted representation using `internal/hashutil`, for Bun, Node, UV and Go.
Apply the same identity calculation to installation, read-only resolution,
verification, seeding and source-farm health. Define whether reserved keys that
cannot affect the installer should be excluded. Hash symbolic `${APP_DIR}`
values rather than the resolved hash-containing path to avoid circular identity;
account for `${STORE}` expansion when defining portability of installed output.

Add tests proving a build-affecting `env` change moves the install path and
triggers installation, while map iteration order cannot move it. Keep
`runtimeEnv` and `dependsOn` outside install identity: they describe execution
and availability, and `runtimeEnv` already never reaches installers. This entry
does not propose hashing inherited process environment or changing JVM downloads.
