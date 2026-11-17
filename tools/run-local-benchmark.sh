#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:?before or after}
warm=${2:-120}
requests=${3:-100}
timeout 240s "/root/.cache/aistudio-upgrade/$version-bench" \
  -rpm 1000 -requests "$requests" -warm "$warm" -label "$version-warm$warm" \
  -output "benchmark-$version-$warm.json" 2>"benchmark-$version-$warm.stderr.log"
