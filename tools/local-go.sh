#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="${AISTUDIO_GO_ROOT:-/root/.cache/aistudio-upgrade/go}/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
export CGO_ENABLED=1
export CGO_CPPFLAGS="-I${AISTUDIO_NATIVE_LIBS:-/root/.cache/aistudio-upgrade}/spidermonkey/include -I${AISTUDIO_NATIVE_LIBS:-/root/.cache/aistudio-upgrade}/libwebp/include"
export CGO_LDFLAGS="-L${AISTUDIO_NATIVE_LIBS:-/root/.cache/aistudio-upgrade}/spidermonkey/lib -L${AISTUDIO_NATIVE_LIBS:-/root/.cache/aistudio-upgrade}/libwebp/lib"
if [[ ${1:-} == check-compat ]]; then
  set -- test -run 'Test(Upgrade|EmptyOutput|CompactChat|UpstreamErrors|ManualModel|StatusSnapshot|Recording|LogOutputs|OutputLimit|CallerPermission|PermissionDeniedOther|Other403|Admission)' ./internal/app ./internal/api ./internal/requestdb ./internal/config ./internal/aistudio
fi
exec go "$@"
