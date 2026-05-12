# Contributing

## Releases

Releases are automated via GitHub Actions ([release.yml](.github/workflows/release.yml)).

### Stable release

```bash
git tag v1.0.0
git push origin v1.0.0
```

Publishes: GitHub Release, npm, Docker (GHCR + Docker Hub), Homebrew cask.

### Release candidate

```bash
git tag v1.0.0-rc.1
git push origin v1.0.0-rc.1
```

Same as stable, but Homebrew cask update is skipped (prerelease detected automatically).

### Unstable release

Actions > Release > Run workflow > release_type: `unstable`.

Publishes: npm (`unstable` tag), Docker (GHCR only). No GitHub Release or Homebrew.

### Distribution channels

| Channel        | Stable          | RC               | Unstable   |
| -------------- | --------------- | ---------------- | ---------- |
| GitHub Release | yes             | yes (prerelease) | opt-in     |
| npm            | `latest` / `rc` | `rc`             | `unstable` |
| Docker Hub     | yes             | yes              | no         |
| GHCR           | yes             | yes              | yes        |
| Homebrew       | yes             | no               | no         |
