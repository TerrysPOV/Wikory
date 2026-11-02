#!/usr/bin/env bash
# Proves the scrubber refuses every residue shape and never echoes the offending text.
set -uo pipefail
cd "$(dirname "$0")/.."
d=$(mktemp -d); trap 'rm -rf "$d"' EXIT
fail=0
check() { # label input-text
  printf '%s\n' "$2" > "$d/in"
  out=$(scripts/scrub-fixture.sh "$d/in" "$d/out" 2>&1); rc=$?
  if [ "$rc" -ne 1 ] || [ -e "$d/out" ] || printf '%s' "$out" | grep -qF -- "$3"; then
    echo "FAIL  $1 (rc=$rc)"; fail=1
  else echo "ok    $1"; fi
}
q=$(printf 'Q%.0s' $(seq 1 20)); z=$(printf 'z%.0s' $(seq 1 44)); h=$(printf 'a%.0s' $(seq 1 40))
check "github pat"   "x github""_pat_${q} y"        "${q}"
check "slack"        "x xo""xb-${q} y"              "${q}"
check "jwt"          "x ey""J${q}.${q} y"           "${q}"
check "aws"          "x AK""IA${q} y"               "${q}"
check "bearer"       "Authorization: Bear""er ${q}" "${q}"
check "password="    "pass""word=${q}"              "${q}"
check "token:"       "to""ken: ${q}"                "${q}"
check "base64 run"   "blob ${z} end"                "${z}"
check "hex run"      "oid ${h} end"                 "${h}"
check "email"        "mail someone@example.com"     "someone@example.com"
printf '{"cwd":"%s/x"}\n' "$HOME" > "$d/in"
scripts/scrub-fixture.sh "$d/in" "$d/out" >/dev/null 2>&1 && grep -q '"/HOME/x"' "$d/out" && echo "ok    home path replaced" || { echo "FAIL  home path"; fail=1; }
# Fail-closed checks: operational failures must never end in a success message or exit 0.
for label in "missing input" "unwritable output"; do
  rm -f "$d/out"
  if [ "$label" = "missing input" ]; then scripts/scrub-fixture.sh "$d/nope" "$d/out" >"$d/log" 2>&1; rc=$?
  else printf 'clean\n' > "$d/in"; scripts/scrub-fixture.sh "$d/in" "$d/no-such-dir/out" >"$d/log" 2>&1; rc=$?; fi
  if [ "$rc" -eq 0 ] || grep -q 'scrub: ok' "$d/log"; then echo "FAIL  $label reported success (rc=$rc)"; fail=1; else echo "ok    $label fails closed (rc=$rc)"; fi
done
exit $fail
