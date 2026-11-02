#!/usr/bin/env bash
# S10: one ledger-counted interactive launch with the same env -i discipline as run-claude.sh.
# It sends no prompt to the model. If Claude Code shows a folder-trust prompt, the script stops
# (exit 4) and S10's items are recorded not-verifiable-here: answering a consent prompt is not
# something a spike does.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
n=$(ledger_take claude s10 interactive)
mkdir -p "$SPIKE_S/out/s10"
spike_load_env
set +e
env -i "${SPIKE_ENVS[@]}" SPIKE_OUT="$SPIKE_S/out/s10" expect "$(dirname "$0")/drive-interactive.exp" "$SPIKE_S"
rc=$?
set -e
printf '%s\tusage\t{"interactive_exit":%s,"model_calls":0}\n' "$n" "$rc" >> "$SPIKE_S/ledger-usage.tsv"
spike_relocate_transcripts
echo "launch $n/$SPIKE_BUDGET label=s10 exit=$rc"
exit $rc
