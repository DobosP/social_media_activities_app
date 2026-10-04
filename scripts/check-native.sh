#!/usr/bin/env bash
# Project checks use native Go. Explicit database/codec fixtures qualify the
# release separately; skipped DB cases here never qualify a release.
set -euo pipefail
native_go=${1:-go}
native_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
export GOWORK=off GOMAXPROCS="${GOMAXPROCS:-2}" GOFLAGS="${GOFLAGS:--p=2}"
native_gofmt=$(dirname -- "$(command -v "$native_go")")/gofmt
test -z "$("$native_gofmt" -l "$native_root/services/server" "$native_root/services/authcore" "$native_root/services/agentapi")"
"$native_go" -C "$native_root/services/server" run ./cmd/check-authcore
for native_module in services/server services/authcore services/agentapi; do
  "$native_go" -C "$native_root/$native_module" vet ./...
  "$native_go" -C "$native_root/$native_module" test -race ./...
done
git -C "$native_root" diff --check
