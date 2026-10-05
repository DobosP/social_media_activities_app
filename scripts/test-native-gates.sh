#!/usr/bin/env bash
# Regression harness for the fail-closed native gates. Runs the real
# qualify-native.sh and check-native.sh against stub go/docker/gofmt/node/uname
# executables on a restricted PATH without ripgrep. No docker, network or
# database. NATIVE_GATES_UNDER_TEST_DIR may name another directory holding both
# scripts (used to prove each guard case fails against the unfixed gates).
set -euo pipefail
gates_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
gates_dir=${NATIVE_GATES_UNDER_TEST_DIR:-$gates_root/scripts}
gates_bash=$(command -v bash)
gates_tmp=$(mktemp -d)
trap 'rm -rf -- "$gates_tmp"' EXIT

# Write an executable bash stub: stub DIR NAME BODY.
stub() {
  mkdir -p "$1"
  printf '#!%s\n%s\n' "$gates_bash" "$3" >"$1/$2"
  chmod +x "$1/$2"
}

# Restricted PATH: exec wrappers for only the real tools the gates need. Wrappers,
# not symlinks, because MSYS copies on ln -s and the copies lose their runtime DLLs.
gates_bin=$gates_tmp/bin
for gates_tool in cat chmod dirname git grep id mkdir; do
  gates_real=$(command -v "$gates_tool") || { echo "missing host tool: $gates_tool" >&2; exit 2; }
  stub "$gates_bin" "$gates_tool" "exec $(printf '%q' "$gates_real") \"\$@\""
done
if PATH=$gates_bin "$gates_bash" -c 'command -v rg' >/dev/null 2>&1; then
  echo 'ripgrep leaked onto the restricted PATH' >&2
  exit 2
fi
# check-native.sh ends with git diff --check; point it at this real worktree.
GIT_DIR=$(git -C "$gates_root" rev-parse --absolute-git-dir)
GIT_WORK_TREE=$gates_root
export GIT_DIR GIT_WORK_TREE

gates_total=0
gates_passed=0
# expect NAME zero|nonzero STATUS [FILE NEEDLE]
expect() {
  local verdict=ok
  gates_total=$((gates_total + 1))
  case $2 in
    zero) (($3 == 0)) || verdict="expected exit 0, got $3";;
    nonzero) (($3 != 0)) || verdict='expected non-zero exit, got 0';;
  esac
  if [[ $verdict == ok && $# -ge 5 ]] && ! grep -qF -- "$5" "$4"; then
    verdict="output lacks '$5'"
  fi
  if [[ $verdict == ok ]]; then
    gates_passed=$((gates_passed + 1))
    echo "ok   $1"
  else
    echo "FAIL $1: $verdict"
  fi
}

# --- qualify-native.sh: stub go builds empty binaries, stub docker replays a log.
qualify_bin=$gates_tmp/qualify-bin
stub "$qualify_bin" uname 'echo "${NATIVE_GATES_UNAME:-Linux}"'
stub "$qualify_bin" docker 'exec cat -- "$NATIVE_GATES_DOCKER_LOG"'
stub "$qualify_bin" go 'out=
while (($#)); do
  if [[ $1 == -o && $# -ge 2 ]]; then out=$2; shift; fi
  shift
done
[[ -n $out ]] && : >"$out"
exit 0'
printf '%s\n' '=== RUN   TestA' '--- PASS: TestA (0.00s)' '=== RUN   TestX' \
  '    x_test.go:9: explicit DSN not supplied' '--- SKIP: TestX (0.00s)' 'PASS' >"$gates_tmp/skip.log"
printf '%s\n' 'testing: warning: no tests to run' 'PASS' >"$gates_tmp/none.log"
printf '%s\n' '=== RUN   TestA' '--- PASS: TestA (0.00s)' '=== RUN   TestB' \
  '--- PASS: TestB (0.01s)' 'PASS' >"$gates_tmp/pass.log"

# run_qualify CASE LOG UNAME
run_qualify() {
  local status=0
  PATH=$gates_bin:$qualify_bin NATIVE_GATES_DOCKER_LOG=$gates_tmp/$2 NATIVE_GATES_UNAME=$3 \
    "$gates_bash" "$gates_dir/qualify-native.sh" "$qualify_bin/go" social-native:fixture fixture-net \
    'postgres://fixture:fixture@db.invalid:5432/fixture' "$gates_tmp/scratch-$1" \
    >"$gates_tmp/$1.out" 2>"$gates_tmp/$1.err" || status=$?
  return "$status"
}
gates_status=0; run_qualify a skip.log Linux || gates_status=$?
expect 'a qualify: --- SKIP line fails the lane' nonzero "$gates_status" "$gates_tmp/a.err" 'Unqualified skipped native test'
gates_status=0; run_qualify b none.log Linux || gates_status=$?
expect 'b qualify: zero --- PASS lines fail the lane' nonzero "$gates_status" "$gates_tmp/b.err" 'no native tests ran in configuration'
gates_status=0; run_qualify c pass.log Linux || gates_status=$?
expect 'c qualify: only --- PASS lines pass' zero "$gates_status" "$gates_tmp/c.out" 'configuration: 2 native tests passed; no skips'
gates_status=0; run_qualify i pass.log Darwin || gates_status=$?
expect 'i qualify: non-Linux host is refused' nonzero "$gates_status" "$gates_tmp/i.err" 'must run on a Linux host'

# --- check-native.sh: stub go reports a temp GOROOT whose bin/ holds gofmt (or not).
stub "$gates_tmp/node-bin" node 'echo "node-stub $*"'
# run_check CASE GOFMT_BODY|- NODE(yes|no)
run_check() {
  local status=0 goroot=$gates_tmp/goroot-$1 path=$gates_bin
  stub "$goroot/bin" go "case \${1:-} in
  env) [[ \${2:-} == GOROOT ]] && { printf '%s\\n' $(printf '%q' "$goroot"); exit 0; };;
  -C) case \${3:-} in run|vet|test) exit 0;; esac;;
esac
echo \"go-stub: unexpected \$*\" >&2
exit 2"
  [[ $2 == - ]] || stub "$goroot/bin" gofmt "$2"
  path=$path:$goroot/bin
  [[ $3 == no ]] || path=$path:$gates_tmp/node-bin
  PATH=$path "$gates_bash" "$gates_dir/check-native.sh" go \
    >"$gates_tmp/$1.out" 2>"$gates_tmp/$1.err" || status=$?
  return "$status"
}
gates_status=0; run_check d - yes || gates_status=$?
expect 'd check: GOROOT without bin/gofmt fails' nonzero "$gates_status" "$gates_tmp/d.err" 'gofmt not found'
gates_status=0; run_check e 'echo services/server/internal/fixture/unformatted.go' yes || gates_status=$?
expect 'e check: gofmt listing a file fails' nonzero "$gates_status"
gates_status=0; run_check f 'exit 2' yes || gates_status=$?
expect 'f check: gofmt erroring silently fails' nonzero "$gates_status"
gates_status=0; run_check g 'exit 0' no || gates_status=$?
expect 'g check: node absent fails' nonzero "$gates_status" "$gates_tmp/g.err" 'node is required'
gates_status=0; run_check h 'exit 0' yes || gates_status=$?
expect 'h check: clean gofmt plus node runs the worker test' zero "$gates_status" "$gates_tmp/h.out" 'meetups-worker.test.mjs'

echo "native gate harness: $gates_passed/$gates_total passed"
((gates_passed == gates_total))
