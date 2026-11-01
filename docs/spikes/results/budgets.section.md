## Measured budgets (1a)

Measured on 2026-11-02 by `go test -count=1 -v ./internal/app/` (macOS arm64, no race detector, `WIKORY_LATENCY_SLACK` unset). These are the first measurements for spec 14.2's Stop-hook latency budget; CI repeats them in its non-race step with `WIKORY_LATENCY_SLACK=3`.

| Budget | Bound | Measured |
|---|---|---|
| SessionStart, cold index, fixture wiki of 300 pages with 8 claims each | 2 s hard deadline | 466 ms |
| SessionStart, warm index, same wiki | 2 s hard deadline | 192 ms |
| Stop capture (deterministic record, one turn) | target 300 ms, test bound 1 s | 39 ms |
