#!/bin/sh
# Runs inside the digest-pinned linux/amd64 Node image in Taskfile.yaml.
# Installs git + pnpm, clones ovineko/ovineko, and writes all three recordings.
set -e

apt-get update -qq
apt-get install -y -qq git asciinema

npm install -g pnpm@12.14.0 --silent

git clone --depth 1 https://github.com/ovineko/ovineko /repo

cd /repo
pnpm install --silent
pnpm turbo run build

node /scripts/capture-demo.ts /repo --output-dir /output
