# Bootstrap dry run on this repository (2026-11-04)

A free run of `wikory bootstrap plan --sample 5` on a throwaway clone of `main`, with the binary built from the `feat/stage2-bootstrap` branch at `b520cb9` plus the Task 16 test files. `plan` is deterministic and sends nothing to a model. No `confirm`, `next`, `submit` or `finish` was run, so nothing paid started and no model saw any source text.

## Setup

The clone was of `main` (stages 0 and 1a), with `origin/HEAD` pointed at `main` so that the branch check passed. The committed `project-memory/` was removed first, because `init` records a pre-existing `project-memory/` as untrusted and there is no trust command until stage 1d; a project that has never run Wikory has none. `wikory init` then wrote a fresh one, and that was committed so that the tree was clean. The data directory was a scratch directory. The clone, the binary and the data directory were all outside the working tree and were removed afterwards.

## Result

| Measure | Value |
|---|---|
| Wall time of `plan` | 7.4 s (3.9 s user, 2.5 s system) |
| Slowest stage | Inventory: 3.8 s to read and scan 311 files (the progress lines on stderr) |
| Sources queued | 288 (203 implementation, 85 attributed) |
| Sources skipped | 23: `secret` 16, `wikory` 5, `generated` 1, `oversize` 1 |
| Components | 23 found, 12 inspected (to be verified) |
| Extract stage | 97 items, about 200,696 input and 67,900 output tokens |
| Verify stage | 12 items, about 72,000 input and 9,600 output tokens |
| Batches | 15 |
| Sample | 5 of the 97 extract items |
| Price | None shown: no price is configured and a subscription plan has none |
| Plan hash | `d94951563a99ba0c63fffcf6e20b6bb13126a27677a4b99ce056facf71b1ccc7` |

Token figures are estimates (bytes divided by four), labelled as such in the output, and not yet measured. The plan hash was the same on a second `plan` run (`--json`), which made a new run id: the hash depends on what would be done, not on the run.

### Why 200,696 input tokens and not the plan's about 189k

The plan's reference run printed about 189k extract input tokens; this run printed 200,696 (11.7k more, about 6%). The cause is unexplained, but these causes are ruled out:

- The figure is `bytes / 4 + 400` per item, from the sizes of queued sources and of each inspected component's files (a digest is capped at 60 KiB). It does not depend on digest framing, on the work files or on the scanner, so the review fixes to digest framing and hostile-name skips cannot move it.
- `plan` was re-run on the same clone of `main` with a binary built from each of the 28 commits from `cc981fb` (the first with a `plan` command) to `abdee5e`. All 28 print 200,696 input tokens, 97 extract items, 288 queued sources and 16 skipped for secret, so no later inventory or review change moved the figure.
- `main` has not moved since 2026-11-02, so the repository input is the same.

What remains is that the reference came from a scratch copy taken while the plan was being written, before the estimate code existed (`6d29cbc`), and that copy cannot be reproduced. Treat 189k as an unreproduced reference and 200,696 as the figure for this tree.

The plan said, as it must: "Nothing has been sent to a model. A person must say yes before any paid step."

## Checks the brief names

- `Components` includes `internal/store` (rank 4), `internal/app` (rank 2), `plugins` (rank 14, not inspected) and `scripts` (rank 8).
- `Sources` shows `project-memory/` as `skipped:wikory`: five files (`.gitattributes`, `config.toml`, `log.md`, `schema/claims.md`, `wiki/README.md`).
- No secret text appeared: `rg -c 'hunter|AKIA|BEGIN'` over stdout and stderr printed nothing (exit 1, no match).
- The run was on the default branch with a clean tree, as `plan` requires.

## Components by rank

| Rank | Component | Score | Inspected |
|---|---|---|---|
| 1 | docs | 52 | yes |
| 2 | internal/app | 42 | yes |
| 3 | internal/decode | 33 | yes |
| 4 | internal/store | 32 | yes |
| 5 | internal/spikes | 27 | yes |
| 6 | cmd/wikory | 24 | yes |
| 7 | internal/spikes/recorder | 23 | yes |
| 8 | scripts | 23 | yes |
| 9 | internal/retrieve | 19 | yes |
| 10 | internal/confine | 15 | yes |
| 11 | internal/capture | 14 | yes |
| 12 | internal/publish | 12 | yes |
| 13 | internal/gitx | 11 | no |
| 14 | plugins | 8 | no |
| 15 | internal/claim | 7 | no |
| 16 | internal/validate | 5 | no |
| 17 | internal/decode/claude | 4 | no |
| 18 | internal/ids | 2 | no |
| 19 | internal/injectcheck | 2 | no |
| 20 | internal/redact | 2 | no |
| 21 | . | 1 | no |
| 22 | .github | 1 | no |
| 23 | internal/deps | 1 | no |

## What was skipped for secret-shaped text, and why it deserves a look

Sixteen files were skipped as `secret`, which matches the reference run in the plan (16). Ten are expected: seven documents that quote secret-shaped fixtures (six spike results and `docs/verified-capabilities.md`), and three source files that define or exercise the redaction patterns (`internal/redact/redact.go`, `internal/redact/redact_test.go`, `internal/capture/capture_test.go`). In the other six, only the line that matched was inspected, and in each it is ordinary code; the rest of those files was not read for secrets, so this is a judgement about the matching line, not a clean bill for the file. The detector is shape-based (Known limit 17 in the plan), and these lines match the assignment shape because a name contains "token":

| File | Line | Shape that matched (the line is ordinary code) |
|---|---|---|
| `internal/store/schema.go` | 51 | `tokenize = 'unicode61');` in the FTS5 table definition |
| `internal/app/project.go` | 68 | `c.Retrieval.BudgetTokens = defaultBudgetTokens` |
| `internal/app/init_cmd.go` | 76 | `cfg.Retrieval.BudgetTokens = defaultBudgetTokens` |
| `internal/app/briefing.go` | 127 | `BudgetTokens: p.Config.Retrieval.BudgetTokens,` |
| `internal/publish/publish.go` | 600 | `token := []byte("job=" + m.JobID)` |
| `internal/spikes/fts5_spike_test.go` | 40 | `... fts5(title, body, tokenize='unicode61')` |

This is safe (a skipped file is never read by a model and the coverage report lists it) but it costs coverage: the `internal/store`, `internal/app` and `internal/publish` components lose core files (`schema.go`, `project.go`, `init_cmd.go`, `briefing.go`, `publish.go`) from their digests and from extraction, so a real bootstrap of this repository would know less about them than the component ranking suggests. The class is wider than these six: identifier values such as `max_tokens`, `tokenizer` and `BudgetTokens` match the assignment rule, the whole file is skipped as secret (fail closed), and the coverage report lists them as `skipped:secret` with no marker for false positives, so expect it on any codebase that handles LLM tokens (Known limit 21 in the plan). Nothing was changed here; this is a finding for Terry to rule on (for example, treating a name that merely contains "token" as a secret key only when the value looks like one).

## Windows

The Windows leg of the kill test (`internal/app/bootstrap_kill_windows_test.go`, a kill is `os.Exit(77)`) has been compiled and vetted only (`GOOS=windows go vet ./internal/app/` and `go test -c`); it has not been run. A Windows failure of the kill tests would first show in the `windows-latest` job of CI.

## Not done

- No sample, no extraction, no verification, no finish: all of those are paid or follow a paid step.
- The manual check `V-bootstrap-1` (subagent access to the private store path) is not part of this task and is not recorded here.
