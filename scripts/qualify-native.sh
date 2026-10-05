#!/usr/bin/env bash
# Compile native regression binaries and run them with release codecs on an
# explicitly supplied disposable database. Does not discover local credentials.
set -euo pipefail
# Linux test binaries are built here and executed inside the Linux release image.
if [[ $(uname -s) != Linux ]]; then
  echo 'qualify-native.sh must run on a Linux host' >&2
  exit 2
fi
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
# Verify exporter contracts through an actual independently running Go sidecar.
CGO_ENABLED=0 "$native_go" -C "$native_root/services/agentapi" build -trimpath -o "$native_scratch/tests/agentapi" .
for native_package in configuration accounts admin app backup booking budgets catalog commands contracts discovery donations export jobs media messaging notifications recommendations safety social web; do
  native_flags=()
  native_source="./internal/$native_package"
  native_workdir="/src/services/server/internal/$native_package"
  case $native_package in
    configuration) native_source=./cmd/social-server; native_workdir=/src/services/server/cmd/social-server; native_flags=(-configuration-test-dsn "$native_dsn");;
    accounts) native_flags=(-accounts-test-dsn "$native_dsn");;
    admin) native_flags=(-admin-test-dsn "$native_dsn");;
    app) native_flags=(-app-test-dsn "$native_dsn");;
    booking|donations|notifications) native_flags=(-domain-test-dsn "$native_dsn");;
    budgets) native_flags=(-budgets-test-dsn "$native_dsn");;
    catalog) native_flags=(-catalog-test-dsn "$native_dsn");;
    commands) native_flags=(-commands-test-dsn "$native_dsn");;
    discovery) native_flags=(-discovery-test-dsn "$native_dsn");;
    export) native_flags=(-export-test-dsn "$native_dsn" -agentapi-test-binary /scratch/tests/agentapi);;
    jobs) native_flags=(-jobs-test-dsn "$native_dsn");;
    media) native_flags=(-media-test-dsn "$native_dsn");;
    messaging) native_flags=(-messaging-test-dsn "$native_dsn");;
    recommendations) native_flags=(-recommendations-test-dsn "$native_dsn");;
    safety) native_flags=(-safety-test-dsn "$native_dsn");;
    social) native_flags=(-social-test-dsn "$native_dsn");;
    web) native_flags=(-web-domain-test-dsn "$native_dsn");;
  esac
  "$native_go" -C "$native_root/services/server" test -race -c \
    -o "$native_scratch/tests/$native_package.test" "$native_source"
  native_log="$native_scratch/tests/$native_package.log"
  if ! docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --user "$(id -u):$(id -g)" --network "$native_network" \
    -e TMPDIR=/scratch/test-tmp -e GOMAXPROCS="${GOMAXPROCS:-2}" \
    -v "$native_root:/src:ro" -v "$native_scratch:/scratch" \
    -w "$native_workdir" "$native_image" \
    "/scratch/tests/$native_package.test" -test.v -test.timeout=10m "${native_flags[@]}" >"$native_log" 2>&1; then
    cat "$native_log" >&2
    exit 1
  fi
  # Fail closed: grep exit 0 is a skip, 1 is none, anything else is an error.
  native_status=0
  grep -q -e '--- SKIP:' -- "$native_log" || native_status=$?
  case $native_status in
    0)
      cat "$native_log" >&2
      echo "Unqualified skipped native test in $native_package" >&2
      exit 1;;
    1) ;;
    *)
      echo "Could not scan $native_log for skipped tests (grep exit $native_status)" >&2
      exit "$native_status";;
  esac
  native_status=0
  native_passed=$(grep -c -e '^--- PASS:' -- "$native_log") || native_status=$?
  case $native_status in
    0) ;;
    1)
      echo "no native tests ran in $native_package" >&2
      exit 1;;
    *)
      echo "Could not count $native_log passing tests (grep exit $native_status)" >&2
      exit "$native_status";;
  esac
  if ! [[ $native_passed =~ ^[0-9]+$ ]] || ((native_passed == 0)); then
    echo "no native tests ran in $native_package" >&2
    exit 1
  fi
  echo "$native_package: $native_passed native tests passed; no skips"
done
