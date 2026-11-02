#!/usr/bin/env bash
# Rebuilds docs/verified-capabilities.md from docs/spikes/results/. Each spike writes only its
# own result files; this is the single writer of the table.
#   results/<ID>.md       one table row:  | V01 | item | method | result | evidence | consequence | status |
#   results/*.section.md  a section appended after the table (budgets, fault logs, end to end)
set -euo pipefail
cd "$(dirname "$0")/../.."
out=docs/verified-capabilities.md
{
  cat docs/spikes/results/_header.md
  echo
  echo "| ID | Item (spec 14.1) | Method | Result | Evidence | Design consequence | Status |"
  echo "|---|---|---|---|---|---|---|"
  for f in $(ls docs/spikes/results/V*.md 2>/dev/null | sort -V); do grep -m1 '^| V' "$f"; done
  for f in $(ls docs/spikes/results/*.section.md 2>/dev/null | sort); do echo; cat "$f"; done
} > "$out"
echo "assembled $out from $(ls docs/spikes/results/V*.md 2>/dev/null | wc -l | tr -d ' ') rows"
