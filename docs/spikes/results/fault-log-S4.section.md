# Fault log S4 (launch ledger)

| Row | Fault | Verdict |
|---|---|---|
| S4-1 | the ledger budget check removed (`if [ "$n" -gt "$SPIKE_BUDGET" ]; then` to `if false; then`) | RED as predicted (`the 13th launch was not refused: exit 0 "13"`, `TestLedgerRefusesTheThirteenthLaunch`) |
| S4-2 | the ledger lock removed (`until mkdir "$lock" ...` to `:`) | RED as predicted (`parallel takes recorded 18 launches, want 12`, `TestLedgerHoldsUnderParallelTakes`) |

Both tests existed only in the plan until 2026-11-02; they now live in `internal/spikes/ledger_spike_test.go` (build tags `spike && unix`) and run against a throwaway ledger under `t.TempDir()`, never the real one.
