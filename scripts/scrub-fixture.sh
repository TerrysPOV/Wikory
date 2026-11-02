#!/usr/bin/env bash
# Scrub a captured payload or transcript before it is committed as a fixture.
# Usage: scripts/scrub-fixture.sh IN OUT
# On a residue it prints only the file, the line number and the rule name, never the line,
# and deletes OUT. Exit 0 clean, 1 residue found, 2 usage or any operational failure (missing or
# unreadable input, failed transform, failed rule load, scanner error). It fails CLOSED: a success
# message is printed only when every step succeeded and every rule ran.
set -uo pipefail
[ $# -eq 2 ] || { echo "usage: scrub-fixture.sh IN OUT" >&2; exit 2; }
in=$1 out=$2
[ -f "$in" ] && [ -r "$in" ] || { echo "scrub: input missing or unreadable" >&2; exit 2; }
scratch=${WIKORY_SPIKE_SCRATCH:-/nonexistent-scratch}
resolved_scratch=$(cd "$scratch" 2>/dev/null && pwd -P || echo /nonexistent-scratch)
user=$(id -un) || { echo "scrub: cannot determine user" >&2; exit 2; }
host=$(hostname -s) || { echo "scrub: cannot determine host" >&2; exit 2; }
[ -n "$user" ] && [ -n "$host" ] && [ -n "${HOME:-}" ] || { echo "scrub: empty identity value" >&2; exit 2; }
# Replacements first: paths and identities, then known noisy-but-harmless fields.
if ! sed -E -e "s#${resolved_scratch}#/SCRATCH#g" -e "s#${scratch}#/SCRATCH#g" \
    -e "s#${HOME}#/HOME#g" -e "s#${user}#user#g" -e "s#${host}#host#g" \
    -e 's#"signature":"[^"]*"#"signature":"SCRUBBED"#g' "$in" > "$out"; then
  rm -f "$out"; echo "scrub: transform failed" >&2; exit 2
fi
[ -f "$out" ] || { echo "scrub: output not written" >&2; exit 2; }
# Residue rules: name<TAB>extended regex.
rules=$(cat <<'RULES'
users-path	/Users/
home-path	/home/[a-z]
email	[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+\.[A-Za-z]{2,}
openai-style-key	sk-[A-Za-z0-9_-]{8,}
github-token	gh[pousr]_[A-Za-z0-9]{8,}
github-pat	github_pat_[A-Za-z0-9_]{8,}
slack-token	xox[baprs]-[A-Za-z0-9-]{6,}
jwt	eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{4,}
aws-key	(AKIA|ASIA)[0-9A-Z]{12,}
private-key	BEGIN [A-Z ]*PRIVATE KEY
bearer	[Bb]earer [A-Za-z0-9._~+/=-]{8,}
assignment	([Pp]ass(word)?|passwd|[Ss]ecret|[Tt]oken|[Aa]pi[_-]?[Kk]ey)["']?[[:space:]]*[:=][[:space:]]*["']?[^[:space:]"']{6,}
long-base64	[A-Za-z0-9+/]{40,}={0,2}
long-hex	[0-9a-fA-F]{40,}
RULES
)
found=0 ran=0 expected=$(printf '%s\n' "$rules" | grep -c .)
[ "$expected" -ge 10 ] || { rm -f "$out"; echo "scrub: rule list failed to load" >&2; exit 2; }
while IFS=$'\t' read -r name re; do
  [ -n "$name" ] || continue
  matches=$(grep -nE -- "$re" "$out"); grc=$?
  if [ "$grc" -gt 1 ]; then rm -f "$out"; echo "scrub: scanner error in rule $name" >&2; exit 2; fi
  ran=$((ran + 1))
  hits=$(printf '%s' "$matches" | cut -d: -f1 | tr '\n' ' ')
  if [ -n "$hits" ]; then
    printf 'scrub: %s: rule %s matched at line(s) %s\n' "$in" "$name" "$hits" >&2
    found=1
  fi
done <<< "$rules"
[ "$ran" -eq "$expected" ] || { rm -f "$out"; echo "scrub: only $ran of $expected rules ran" >&2; exit 2; }
if [ "$found" -ne 0 ]; then
  rm -f "$out"
  echo "scrub: refused; extend the replacement rules for these shapes, never commit unscrubbed" >&2
  exit 1
fi
echo "scrub: ok $out"
