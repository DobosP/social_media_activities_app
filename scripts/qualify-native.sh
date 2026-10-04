#!/usr/bin/env bash
# Compile native regression binaries and run them with release codecs on an
# explicitly supplied disposable database. Does not discover local credentials.
set -euo pipefail
if [[ $# != 5 ]]; then
  echo 'Usage: qualify-native.sh GO_EXECUTABLE RELEASE_IMAGE PRIVATE_NETWORK DISPOSABLE_DSN ABSOLUTE_SCRATCH' >&2
  exit 2
fi
native_go=$1
native_image=$2
native_network=$3
native_dsn=$4
native_scratch=$5
native_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
[[ $native_scratch = /* && $native_scratch != / ]] || exit 2
mkdir -p "$native_scratch/tests" "$native_scratch/test-tmp"
chmod 700 "$native_scratch/test-tmp"
export GOWORK=off
for native_package in accounts admin app booking catalog commands discovery donations export jobs media messaging notifications recommendations safety social web; do
  native_flags=()
  case $native_package in
    accounts) native_flags=(-accounts-test-dsn "$native_dsn");;
    admin) native_flags=(-admin-test-dsn "$native_dsn");;
    app) native_flags=(-app-test-dsn "$native_dsn");;
    booking|donations|notifications) native_flags=(-domain-test-dsn "$native_dsn");;
    catalog) native_flags=(-catalog-test-dsn "$native_dsn");;
    commands) native_flags=(-commands-test-dsn "$native_dsn");;
    discovery) native_flags=(-discovery-test-dsn "$native_dsn");;
    export) native_flags=(-export-test-dsn "$native_dsn");;
    jobs) native_flags=(-jobs-test-dsn "$native_dsn");;
    media) native_flags=(-media-test-dsn "$native_dsn");;
    messaging) native_flags=(-messaging-test-dsn "$native_dsn");;
    recommendations) native_flags=(-recommendations-test-dsn "$native_dsn");;
    safety) native_flags=(-safety-test-dsn "$native_dsn");;
    social) native_flags=(-social-test-dsn "$native_dsn");;
    web) native_flags=(-web-domain-test-dsn "$native_dsn");;
  esac
  "$native_go" -C "$native_root/services/server" test -race -c \
    -o "$native_scratch/tests/$native_package.test" "./internal/$native_package"
  native_log="$native_scratch/tests/$native_package.log"
  if ! docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --user "$(id -u):$(id -g)" --network "$native_network" \
    -e TMPDIR=/scratch/test-tmp \
    -v "$native_root:/src:ro" -v "$native_scratch:/scratch" \
    -w "/src/services/server/internal/$native_package" "$native_image" \
    "/scratch/tests/$native_package.test" -test.v -test.timeout=10m "${native_flags[@]}" >"$native_log" 2>&1; then
    cat "$native_log" >&2
    exit 1
  fi
  if rg -q -- '--- SKIP:' "$native_log"; then
    cat "$native_log" >&2
    echo "Unqualified skipped native test in $native_package" >&2
    exit 1
  fi
  echo "$native_package: $(rg -c '^--- PASS:' "$native_log") native tests passed; no skips"
done
