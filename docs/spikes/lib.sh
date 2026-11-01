#!/usr/bin/env bash
# Shared by every stage 0 launch of Claude Code or Codex. Source it; do not run it.
#   spike_root               the scratch area
#   ledger_take PRODUCT LABEL KIND   reserve one launch in the shared ledger, or refuse the 13th
#   ledger_usage N RESULT_JSON       log the usage a launch reported (internal calls are logged, not counted)
#   spike_env                print the env -i arguments every launch uses
# The budget unit is a CLI launch: 12 across all of stage 0, Claude Code and Codex together,
# interactive or not. Retries count. Nothing resets the ledger.
: "${WIKORY_SPIKE_SCRATCH:?set WIKORY_SPIKE_SCRATCH to a scratch directory outside the repository}"
SPIKE_S=$WIKORY_SPIKE_SCRATCH/wikory-spikes
SPIKE_LEDGER=$SPIKE_S/ledger.tsv
SPIKE_BUDGET=12

spike_root() { printf '%s\n' "$SPIKE_S"; }

ledger_take() { # product label kind
  local lock=$SPIKE_S/ledger.lock n
  mkdir -p "$SPIKE_S"
  until mkdir "$lock" 2>/dev/null; do sleep 0.2; done
  n=$(( $( (grep -c '^[0-9]' "$SPIKE_LEDGER" 2>/dev/null) || echo 0) + 1 ))
  if [ "$n" -gt "$SPIKE_BUDGET" ]; then
    rmdir "$lock"
    echo "spike budget exhausted: $SPIKE_BUDGET launches already recorded in $SPIKE_LEDGER" >&2
    return 3
  fi
  printf '%s\t%s\t%s\t%s\t%s\tlaunched\n' "$n" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" "$2" "$3" >> "$SPIKE_LEDGER"
  rmdir "$lock"
  echo "$n"
}

ledger_usage() { # n result.json
  local usage
  usage=$(jq -c '{num_turns, total_cost_usd, usage}' "$2" 2>/dev/null || echo '"usage unavailable"')
  printf '%s\tusage\t%s\n' "$1" "$usage" >> "$SPIKE_S/ledger-usage.tsv"
}

# Isolation: env -i drops the parent session's variables, including its nested-session
# markers. CLAUDE_CONFIG_DIR points at the scratch config only when S4 proved that auth works
# there without copying a credential (the file config-dir-ok exists); otherwise the real one
# is used and the scratch-path session logs land in ~/.claude/projects/<scratch slug>/, which
# spike_relocate_transcripts moves out afterwards. Settings files are never edited.
spike_env() {
  printf '%s\n' "HOME=$HOME" "USER=$USER" "LOGNAME=$USER" "PATH=$SPIKE_S/bin:/opt/homebrew/bin:/usr/bin:/bin" \
    "TERM=xterm-256color" "LANG=en_GB.UTF-8"
  if [ -e "$SPIKE_S/config-dir-ok" ]; then printf '%s\n' "CLAUDE_CONFIG_DIR=$SPIKE_S/claude-config"; fi
}

# Moves the session logs Claude Code wrote for the scratch project out of the real projects
# directory into the scratch area. It touches only directories named after the scratch path.
spike_relocate_transcripts() {
  [ -e "$SPIKE_S/config-dir-ok" ] && return 0
  local slug dir
  slug=$(printf '%s' "$SPIKE_S/proj" | sed 's#[/.]#-#g')
  dir=$HOME/.claude/projects/$slug
  if [ -d "$dir" ]; then
    mkdir -p "$SPIKE_S/relocated-transcripts"
    mv "$dir" "$SPIKE_S/relocated-transcripts/$(date -u +%Y%m%dT%H%M%SZ)"
  fi
}
