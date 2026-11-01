#!/usr/bin/env bash
# End-to-end on Claude Code 2.1.316 with the real plugin. Two model invocations, both taken
# through docs/spikes/run-claude.sh so they are counted in the shared stage 0 ledger.
set -euo pipefail
: "${WIKORY_SPIKE_SCRATCH:?set WIKORY_SPIKE_SCRATCH}"
repo=$(cd "$(dirname "$0")/../.." && pwd)
. "$repo/docs/spikes/lib.sh" # SPIKE_S, the ledger and the isolation environment
E=$WIKORY_SPIKE_SCRATCH/wikory-e2e
P=$SPIKE_S/proj # run-claude.sh always launches from here
rm -rf "$E" && mkdir -p "$E/bin" "$E/data"
(cd "$repo" && go build -o "$E/bin/wikory" ./cmd/wikory)
# run-claude.sh passes only the spike_env variables to Claude Code, and puts $SPIKE_S/bin first
# on PATH. A small launcher there supplies WIKORY_DATA_DIR and runs the freshly built binary.
mkdir -p "$SPIKE_S/bin"
cat > "$SPIKE_S/bin/wikory" <<LAUNCH
#!/bin/sh
export WIKORY_DATA_DIR='$E/data'
exec '$E/bin/wikory' "\$@"
LAUNCH
chmod +x "$SPIKE_S/bin/wikory"
echo '{}' > "$E/settings.json" # no recorder hooks: only the plugin's hooks run
# Set the earlier spike project aside (moved, not deleted) and build the e2e project in its place.
if [ -e "$P" ]; then mv "$P" "$P.pre-e2e-$(date -u +%Y%m%dT%H%M%SZ)"; fi
mkdir -p "$P"
cd "$P"
git init -q && git config user.name e2e && git config user.email e2e@invalid
printf 'package q\n' > q.go && git add q.go && git commit -qm code
export WIKORY_DATA_DIR=$E/data
"$E/bin/wikory" init >/dev/null
cat > project-memory/wiki/retries.md <<'PAGE'
---
type: decision
title: Retries are capped at five
---
# Retries are capped at five
<!-- claim kind=intent status=verified ev=q.go dep=q.go -->
Job retries stop after five attempts so a poison message cannot loop forever.
PAGE
git add project-memory && git commit -qm memory
"$E/bin/wikory" page stamp project-memory/wiki/retries.md
git add project-memory && git commit -qm stamped
run() { # label prompt
  local t0 t1
  t0=$(date +%s)
  SPIKE_SETTINGS=$E/settings.json "$repo/docs/spikes/run-claude.sh" "$1" "$2" --plugin-dir "$repo/plugins/claude"
  t1=$(date +%s)
  jq '{usage, total_cost_usd}' "$SPIKE_S/out/$1/result.json"
  echo "elapsed_seconds=$((t1 - t0)) (includes run-claude.sh's 8 s settle)"
}
run e2e1 'Reply with exactly the word: ok'
sleep 3
run e2e2 'Quote, verbatim, the first line of the wikory status block you were given at the start of this session, and name any decision it lists.'
jq -r .result "$SPIKE_S/out/e2e2/result.json"
"$E/bin/wikory" --json doctor | jq '.data | {mode, pending_turns, registered, trusted}'
