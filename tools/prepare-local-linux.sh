#!/usr/bin/env bash
set -euo pipefail
deps=/root/.cache/aistudio-upgrade
mkdir -p "$deps/spidermonkey" "$deps/libwebp"
if [[ ! -x "$deps/go/bin/go" ]]; then
  curl -fsSL --retry 2 https://go.dev/dl/go1.27.3.linux-amd64.tar.gz | tar -xz -C "$deps"
fi
if [[ ! -f "$deps/spidermonkey/lib/libjs_static.a" ]]; then
  curl -fsSL --retry 2 https://github.com/Mag1cFall/firefox/releases/download/v158.0.1-aistudio2api.2/spidermonkey-v158.0.1-aistudio2api.2-linux-amd64-gnu.tar.gz | tar -xz -C "$deps/spidermonkey"
fi
if [[ ! -f "$deps/libwebp/lib/libwebp.a" ]]; then
  curl -fsSL --retry 2 https://github.com/Mag1cFall/libwebp/releases/download/v1.6.0-aistudio2api.1/libwebp-v1.6.0-aistudio2api.1-linux-amd64-gnu.tar.gz | tar -xz -C "$deps/libwebp"
fi
"$deps/go/bin/go" version
