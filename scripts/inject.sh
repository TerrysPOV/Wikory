#!/usr/bin/env bash
# One-site fault injection with an honest verdict.
# Usage: scripts/inject.sh FILE 'OLD' 'NEW' PACKAGE 'TESTREGEX' 'EXPECTED_FAILURE_TEXT'
#
# Exit 0, RED: the baseline passed, the faulted code compiled, a test selected by TESTREGEX
#   ran and failed, its output contains EXPECTED_FAILURE_TEXT, and nothing panicked.
# Exit 1, SURVIVED: the faulted code compiled and the selected tests ran and passed.
# Exit 2, INCONCLUSIVE: anything else: bad arguments, OLD not found exactly once, a failing
#   or empty baseline, a build failure, a panic, a failure outside a named test, or a
#   failure whose text is not the predicted one. INCONCLUSIVE never counts as RED.
# FILE is always restored.
set -uo pipefail
if [ $# -ne 6 ]; then echo "usage: inject.sh FILE OLD NEW PACKAGE TESTREGEX EXPECTED_FAILURE_TEXT" >&2; exit 2; fi
file=$1 old=$2 new=$3 pkg=$4 run=$5 expect=$6
work=$(mktemp -d)
cp "$file" "$work/backup"
trap 'cp "$work/backup" "$file"; rm -rf "$work"' EXIT
inconclusive() { echo "INCONCLUSIVE: $*" >&2; exit 2; }
tests_run() { jq -r 'select(.Test != null and (.Action == "pass" or .Action == "fail")) | .Test' "$1" | wc -l | tr -d ' '; }

go test -count=1 -json -run "$run" "$pkg" > "$work/base.json" 2>&1
base_rc=$?
[ "$base_rc" -eq 0 ] || inconclusive "the baseline fails before any fault is injected"
[ "$(tests_run "$work/base.json")" -gt 0 ] || inconclusive "no test matching '$run' ran in the baseline"

count=$(OLD="$old" perl -0ne 'BEGIN{$o=$ENV{OLD}} $c += () = /\Q$o\E/g; END{print $c+0}' "$file")
[ "$count" = 1 ] || inconclusive "expected exactly 1 match of the old text in $file, found $count"
OLD="$old" NEW="$new" perl -0pi -e 'BEGIN{$o=$ENV{OLD};$n=$ENV{NEW}} s/\Q$o\E/$n/' "$file"

go test -count=1 -json -run "$run" "$pkg" > "$work/fault.json" 2>&1
rc=$?
if ! jq -e . "$work/fault.json" >/dev/null 2>&1; then inconclusive "go test did not produce JSON (setup failure)"; fi
if jq -e 'select(.Action == "build-fail")' "$work/fault.json" >/dev/null || grep -q '\[build failed\]\|\[setup failed\]' "$work/fault.json"; then
  inconclusive "the faulted code does not compile; a compile error proves nothing about the assertion"
fi
if [ "$rc" -eq 0 ]; then
  [ "$(tests_run "$work/fault.json")" -gt 0 ] || inconclusive "no test ran under the fault"
  echo "SURVIVED: '$run' still passes with the fault in $file" >&2
  exit 1
fi
jq -r 'select(.Action == "fail" and .Test != null) | .Test' "$work/fault.json" | sort -u > "$work/failed.txt"
[ -s "$work/failed.txt" ] || inconclusive "the package failed outside any named test (TestMain, init or the binary)"
jq -r --rawfile f "$work/failed.txt" '($f | split("\n")) as $names | select(.Action == "output" and .Test != null and (.Test as $t | $names | index($t))) | .Output' "$work/fault.json" > "$work/out.txt"
# A panic anywhere in the event stream (any test, package-level, or after a named failure) is never RED,
# and an EXPECTED text that mentions a panic is not an exception.
jq -r 'select(.Action == "output") | .Output' "$work/fault.json" > "$work/all-out.txt"
if grep -q 'panic:' "$work/all-out.txt"; then
  inconclusive "a panic occurred under the fault, so a failing assertion cannot be attributed: $(grep -m1 'panic:' "$work/all-out.txt")"
fi
if ! grep -qF -- "$expect" "$work/out.txt"; then
  inconclusive "the failure text did not contain the prediction '$expect'; first failure line: $(grep -m1 -E '_test\.go:[0-9]+:' "$work/out.txt" | sed 's/^ *//')"
fi
grep -m3 -F -- "$expect" "$work/out.txt" | sed 's/^ */  /'
echo "RED as predicted"
