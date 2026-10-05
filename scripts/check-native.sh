#!/usr/bin/env bash
# Project checks use native Go. Explicit database/codec fixtures qualify the
# release separately; skipped DB cases here never qualify a release.
set -euo pipefail
native_go=${1:-go}
native_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
export GOWORK=off GOMAXPROCS="${GOMAXPROCS:-2}" GOFLAGS="${GOFLAGS:--p=2}"
# Fail closed: a missing Go, gofmt or node is an error, never an empty result.
if ! native_go=$(command -v "$native_go"); then
  echo "check-native.sh: Go executable not found: ${1:-go}" >&2
  exit 2
fi
# Ask the module's own toolchain, which may differ from the first go on PATH.
native_goroot=$("$native_go" -C "$native_root/services/server" env GOROOT)
native_gofmt=$native_goroot/bin/gofmt
if [[ -z $native_goroot || ! -x $native_gofmt ]]; then
  echo "check-native.sh: gofmt not found at GOROOT/bin/gofmt ($native_gofmt)" >&2
  exit 2
fi
if ! command -v node >/dev/null; then
  echo 'check-native.sh: node is required for the offline service-worker test' >&2
  exit 2
fi
native_unformatted=$("$native_gofmt" -l "$native_root/services/server" "$native_root/services/authcore" "$native_root/services/agentapi")
if [[ -n $native_unformatted ]]; then
  echo 'check-native.sh: gofmt reports unformatted files:' >&2
  printf '%s\n' "$native_unformatted" >&2
  exit 1
fi
"$native_go" -C "$native_root/services/server" run ./cmd/check-authcore
for native_module in services/server services/authcore services/agentapi; do
  "$native_go" -C "$native_root/$native_module" vet ./...
  "$native_go" -C "$native_root/$native_module" test -race ./...
done
# TAP output is stable across Node releases; a run with zero passing tests fails.
native_worker=$(node --test --test-reporter=tap "$native_root/services/server/internal/web/assets/meetups-worker.test.mjs")
printf '%s\n' "$native_worker"
native_worker_passed=
while IFS= read -r native_line; do
  native_line=${native_line%$'\r'}
  if [[ $native_line =~ ^#\ pass\ ([0-9]+)$ ]]; then native_worker_passed=${BASH_REMATCH[1]}; fi
done <<<"$native_worker"
if [[ -z $native_worker_passed ]] || ((10#$native_worker_passed == 0)); then
  echo 'check-native.sh: offline service-worker test reported no passing tests' >&2
  exit 1
fi
git -C "$native_root" diff --check
