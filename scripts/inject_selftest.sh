#!/usr/bin/env bash
# Proves inject.sh cannot report a compile error, a panic, a wrong failure or a missing test as RED.
set -uo pipefail
cd "$(dirname "$0")/.."
f=internal/injectcheck/add.go p=./internal/injectcheck/
fail=0
expect() {
  local want=$1 label=$2; shift 2
  scripts/inject.sh "$@" >/dev/null 2>&1; local got=$?
  if [ "$got" -eq "$want" ]; then echo "ok    $label (exit $got)"; else echo "FAIL  $label: want exit $want, got $got"; fail=1; fi
}
expect 0 "real assertion fault is RED"        "$f" 'return a + b' 'return a - b'          "$p" '^TestAdd$' 'Add(2, 3) = -1, want 5'
expect 2 "compile-error fault is INCONCLUSIVE" "$f" 'return a + b' 'return a +'            "$p" '^TestAdd$' 'Add(2, 3)'
expect 2 "panicking fault is INCONCLUSIVE"     "$f" 'return a + b' 'return a + b + Div(1, a-a)' "$p" '^TestAdd$' 'Add(2, 3)'
expect 2 "wrong prediction is INCONCLUSIVE"    "$f" 'return a + b' 'return a - b'          "$p" '^TestAdd$' 'something else entirely'
expect 2 "no matching test is INCONCLUSIVE"    "$f" 'return a + b' 'return a - b'          "$p" '^TestNope$' 'Add(2, 3)'
expect 1 "harmless change SURVIVES"            "$f" 'return a + b' 'return b + a'          "$p" '^TestAdd$' 'Add(2, 3)'
expect 2 "missing old text is INCONCLUSIVE"    "$f" 'return a * b' 'return a - b'          "$p" '^TestAdd$' 'Add(2, 3)'
exit $fail
