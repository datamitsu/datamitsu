---
title: Build from Source
description: Build datamitsu from source using Go
---

# Build from Source

Requires **Go 1.25.2+**.

Clone the repository and build:

```bash
git clone https://github.com/datamitsu/datamitsu.git
cd datamitsu
go build
```

This produces a `datamitsu` binary in the current directory. Move it to a directory in your `PATH`:

```bash
mv datamitsu /usr/local/bin/
```

## Using pnpm

If you have pnpm installed, you can also build via:

```bash
pnpm build
```
