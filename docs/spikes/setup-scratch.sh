#!/usr/bin/env bash
# Builds the spike recorder and lays out the isolated scratch project. No model calls.
set -euo pipefail
: "${WIKORY_SPIKE_SCRATCH:?set WIKORY_SPIKE_SCRATCH to a scratch directory outside the repository}"
repo=$(cd "$(dirname "$0")/../.." && pwd)
S=$WIKORY_SPIKE_SCRATCH/wikory-spikes
mkdir -p "$S/bin" "$S/out" "$S/proj" "$S/plugin"
(cd "$repo" && go build -tags spike -o "$S/bin/spike-recorder" ./internal/spikes/recorder)
rm -rf "$S/plugin/wikoryspike"
cp -R "$repo/docs/spikes/plugin/wikoryspike" "$S/plugin/wikoryspike"
mkdir -p "$S/plugin/wikoryspike/bin"
cp "$S/bin/spike-recorder" "$S/plugin/wikoryspike/bin/spike-recorder"
sed "s#@BIN@#$S/bin#g" "$repo/docs/spikes/settings/base.json" > "$S/settings.json"
echo '{"mcpServers":{}}' > "$S/empty-mcp.json"
if [ ! -d "$S/proj/.git" ]; then
  git -C "$S/proj" init -q
  printf '# spike project\n' > "$S/proj/README.md"
  git -C "$S/proj" add README.md
  git -C "$S/proj" -c user.name=spike -c user.email=spike@invalid commit -qm init
fi
# Isolation check (no model call): does a scratch CLAUDE_CONFIG_DIR keep authentication
# working without copying any credential into it? Only the verdict is kept; the status text
# is never printed.
mkdir -p "$S/claude-config"
rm -f "$S/config-dir-ok"
if env -i HOME="$HOME" USER="$USER" PATH="/opt/homebrew/bin:/usr/bin:/bin" CLAUDE_CONFIG_DIR="$S/claude-config" \
     claude auth status > "$S/auth-status.txt" 2>&1 && ! grep -qiE 'not (logged|signed) in|no auth' "$S/auth-status.txt"; then
  touch "$S/config-dir-ok"
  echo "isolation: scratch CLAUDE_CONFIG_DIR authenticates; session logs stay in the scratch area"
else
  echo "isolation: scratch CLAUDE_CONFIG_DIR does not authenticate; using the real config dir with relocation of scratch session logs"
fi
rm -f "$S/auth-status.txt"
echo "scratch ready: $S"
