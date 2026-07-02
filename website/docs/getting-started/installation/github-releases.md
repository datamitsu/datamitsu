---
title: GitHub Releases
description: Download datamitsu binaries, deb/rpm/apk packages, and verification files directly from GitHub Releases
---

# GitHub Releases

Every stable release on the
[releases page](https://github.com/datamitsu/datamitsu/releases) ships:

- **Archives** — `datamitsu_<version>_<os>_<arch>.tar.gz` (Linux, macOS) and
  `.zip` (Windows) for `amd64` and `arm64`
- **Linux packages** — `.deb`, `.rpm`, and `.apk`
- **`checksums.txt`** — SHA-256 of every asset, plus
  `checksums.txt.sigstore.json`, its keyless
  [cosign](https://docs.sigstore.dev/) signature
- **SBOMs** — one per archive
- **`datamitsu_parsers_<version>.wasm`** — the parser module (downloaded by the
  CLI on demand, verified against the signed `checksums.txt`)
- **`datamitsu-<version>.vsix`** — the [VS Code extension](./vscode.md) for
  manual installs

## Binary archive

```bash
VERSION=0.1.13
curl -LO "https://github.com/datamitsu/datamitsu/releases/download/v${VERSION}/datamitsu_${VERSION}_linux_amd64.tar.gz"
tar -xzf "datamitsu_${VERSION}_linux_amd64.tar.gz"
sudo install -m 0755 datamitsu /usr/local/bin/datamitsu
```

## Linux packages

```bash
# Debian / Ubuntu
sudo dpkg -i datamitsu_${VERSION}_linux_amd64.deb

# Fedora / RHEL
sudo rpm -i datamitsu_${VERSION}_linux_amd64.rpm

# Alpine
sudo apk add --allow-untrusted datamitsu_${VERSION}_linux_amd64.apk
```

## Verify downloads

Check the SHA-256 against `checksums.txt`:

```bash
curl -LO "https://github.com/datamitsu/datamitsu/releases/download/v${VERSION}/checksums.txt"
sha256sum --ignore-missing -c checksums.txt
```

`checksums.txt` itself is signed in CI with keyless cosign. Verify the
signature to establish that the checksums came from this repository's release
workflow:

```bash
curl -LO "https://github.com/datamitsu/datamitsu/releases/download/v${VERSION}/checksums.txt.sigstore.json"
cosign verify-blob \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github.com/datamitsu/datamitsu/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  checksums.txt
```

Release artifacts also carry [GitHub build provenance attestations](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations):

```bash
gh attestation verify datamitsu_${VERSION}_linux_amd64.tar.gz --repo datamitsu/datamitsu
```

See [Supply Chain Security](../../guides/supply-chain-security.md) for the full
verification model.
