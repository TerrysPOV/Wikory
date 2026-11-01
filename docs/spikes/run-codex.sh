#!/usr/bin/env bash
# One isolated, ledger-counted Codex launch. Uses a scratch CODEX_HOME and never copies a
# credential into it: if Codex cannot authenticate there non-interactively, the launch is still
# counted and the S12 items are recorded not-verifiable-here.
# Usage: docs/spikes/run-codex.sh <label> <prompt> [extra codex exec args...]
set -euo pipefail
. "$(dirname "$0")/lib.sh"
label=$1 prompt=$2
shift 2
n=$(ledger_take codex "$label" exec)
out=$SPIKE_S/out/$label
mkdir -p "$out" "$SPIKE_S/codex-home"
spike_load_env
cd "$SPIKE_S/proj"
set +e
env -i "${SPIKE_ENVS[@]}" CODEX_HOME="$SPIKE_S/codex-home" SPIKE_OUT="$out" \
  codex exec --skip-git-repo-check "$@" "$prompt" > "$out/result.txt" 2>&1
rc=$?
set -e
printf '%s\tusage\t{"exit":%s}\n' "$n" "$rc" >> "$SPIKE_S/ledger-usage.tsv"
echo "launch $n/$SPIKE_BUDGET label=$label exit=$rc"
