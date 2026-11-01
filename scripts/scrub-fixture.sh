#!/usr/bin/env bash
# Scrub a captured payload or transcript before it is committed as a fixture.
# Usage: scripts/scrub-fixture.sh IN OUT
# On a residue it prints only the file, the line number and the rule name, never the line,
# and deletes OUT. Exit 0 clean, 1 residue found, 2 usage.
set -uo pipefail
[ $# -eq 2 ] || { echo "usage: scrub-fixture.sh IN OUT" >&2; exit 2; }
in=$1 out=$2
scratch=${WIKORY_SPIKE_SCRATCH:-/nonexistent-scratch}
resolved_scratch=$(cd "$scratch" 2>/dev/null && pwd -P || echo /nonexistent-scratch)
user=$(id -un)
host=$(hostname -s)
# Replacements first: paths and identities, then known noisy-but-harmless fields.
sed -E -e "s#${resolved_scratch}#/SCRATCH#g" -e "s#${scratch}#/SCRATCH#g" \
    -e "s#${HOME}#/HOME#g" -e "s#${user}#user#g" -e "s#${host}#host#g" \
    -e 's#"signature":"[^"]*"#"signature":"SCRUBBED"#g' "$in" > "$out"
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
found=0
while IFS=$'\t' read -r name re; do
  [ -n "$name" ] || continue
  hits=$(grep -nE -- "$re" "$out" 2>/dev/null | cut -d: -f1 | tr '\n' ' ')
  if [ -n "$hits" ]; then
    printf 'scrub: %s: rule %s matched at line(s) %s\n' "$in" "$name" "$hits" >&2
    found=1
  fi
done <<< "$rules"
if [ "$found" -ne 0 ]; then
  rm -f "$out"
  echo "scrub: refused; extend the replacement rules for these shapes, never commit unscrubbed" >&2
  exit 1
fi
echo "scrub: ok $out"
