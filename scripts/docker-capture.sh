#!/bin/sh
# Runs inside Docker (node:26.7.0-trixie-slim@sha256:5758d367d7b4f48b73a9bb3530e687e47efb289f3b43f9c0450a25225ae0db5d, linux/amd64).
# Installs git + pnpm, clones ovineko/ovineko, runs datamitsu, writes cold.cast + warm.cast.
set -e

apt-get update -qq
apt-get install -y -qq git asciinema

npm install -g pnpm@11.5.2 --silent

git clone --depth 1 https://github.com/ovineko/ovineko /repo

cd /repo
pnpm install --silent
pnpm turbo run build

node /scripts/capture-demo.ts /repo --output-dir /output
