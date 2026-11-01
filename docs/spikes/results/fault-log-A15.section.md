# Fault log: A15 (wikory init)

Run with `scripts/inject.sh` on 2026-11-01. Predictions are the plan's.

| # | Fault | Predicted | Verdict |
|---|---|---|---|
| A15-1 | trust granted to a pre-existing project-memory (`if created {` to `if true {`) | RED, `TestInitInExistingProjectMemoryDoesNotGrantTrust` | RED as predicted |
| A15-2 | existing files rewritten (`case errors.Is(err, fs.ErrNotExist):` to `case true:`) | RED, `TestInitIsIdempotent`, publish conflict on the second run | RED as predicted |
| A15-3 | unknown config keys accepted (`DisallowUnknownFields` removed) | RED, `TestConfigRejectsUnknownKeysAndBadIDs` | RED as predicted |

## Follow-up: trust only for an absent or empty project-memory/, and never change a recorded decision

| # | Fault | Predicted | Verdict |
|---|---|---|---|
| A15-4 | emptiness check always reports empty (`pmEmpty = len(ents) == 0 \|\| true`) | RED, `TestInitWithPreexistingWikiPageDoesNotGrantTrust`, `Created:true Trusted:true StoreDir` | RED as predicted |
| A15-5 | emptiness check ignores dotfiles | RED, `TestInitWithLoneDotfileInProjectMemoryDoesNotGrantTrust`, same text | RED as predicted |
| A15-6 | recorded decision not honoured (`case recorded:` to `case recorded && false:`), so init upgrades an Undecided record | RED, `TestInitKeepsRecordedTrustDecision`, same text | RED as predicted |

A first attempt at A15-4 (`pmEmpty = true`) did not compile (`ents` unused) and was INCONCLUSIVE, so it was redone with `|| true`.
