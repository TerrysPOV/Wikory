# Fault log: A15 (wikory init)

Run with `scripts/inject.sh` on 2026-11-01. Predictions are the plan's.

| # | Fault | Predicted | Verdict |
|---|---|---|---|
| A15-1 | trust granted to a pre-existing project-memory (`if created {` to `if true {`) | RED, `TestInitInExistingProjectMemoryDoesNotGrantTrust` | RED as predicted |
| A15-2 | existing files rewritten (`case errors.Is(err, fs.ErrNotExist):` to `case true:`) | RED, `TestInitIsIdempotent`, publish conflict on the second run | RED as predicted |
| A15-3 | unknown config keys accepted (`DisallowUnknownFields` removed) | RED, `TestConfigRejectsUnknownKeysAndBadIDs` | RED as predicted |
