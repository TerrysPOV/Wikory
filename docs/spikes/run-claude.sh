#!/usr/bin/env bash
# One isolated, ledger-counted Claude Code launch for a stage 0 spike.
# Usage: docs/spikes/run-claude.sh <label> <prompt> [extra claude args...]
# Env: SPIKE_STRICT_MCP=0 lets plugin MCP servers load (S7, S8). SPIKE_SETTINGS=<file> overrides settings.
set -euo pipefail
. "$(dirname "$0")/lib.sh"
label=$1 prompt=$2
shift 2
n=$(ledger_take claude "$label" print)
out=$SPIKE_S/out/$label
mkdir -p "$out"
mcp_args=(--strict-mcp-config --mcp-config "$SPIKE_S/empty-mcp.json")
if [ "${SPIKE_STRICT_MCP:-1}" = 0 ]; then mcp_args=(); fi
settings=${SPIKE_SETTINGS:-$SPIKE_S/settings.json}
spike_load_env
cd "$SPIKE_S/proj"
set +e
env -i "${SPIKE_ENVS[@]}" SPIKE_OUT="$out" SPIKE_COPY_TRANSCRIPT=1 \
  claude -p "$prompt" --model haiku --setting-sources project --settings "$settings" \
  ${mcp_args[@]+"${mcp_args[@]}"} --output-format json "$@" > "$out/result.json" 2> "$out/stderr.txt"
rc=$?
set -e
ledger_usage "$n" "$out/result.json"
echo "launch $n/$SPIKE_BUDGET label=$label exit=$rc"
sleep 8 # let detached children (lagcopy, sleepwrite) finish
spike_relocate_transcripts
