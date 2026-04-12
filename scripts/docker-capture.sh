#!/bin/sh
# Runs inside Docker (node:24-slim, linux/amd64).
# Installs git + pnpm, clones ovineko/ovineko, runs datamitsu, writes cold.cast + warm.cast.
set -e

apt-get update -qq
apt-get install -y -qq git asciinema

npm install -g pnpm --silent

git clone --depth 1 https://github.com/ovineko/ovineko /repo

cd /repo
pnpm install --silent
pnpm turbo run build

node /scripts/capture-demo.ts /repo --output-dir /output
