## End-to-end (1a)

Run by `docs/spikes/e2e.sh` on 2026-11-01 (ledger timestamps 22:53:31Z and 22:53:46Z), Claude Code 2.1.316, `--model haiku`, with the real plugin loaded by `--plugin-dir plugins/claude` and a freshly built `wikory` first on `PATH`. The private store was a scratch `WIKORY_DATA_DIR`. Both launches went through `docs/spikes/run-claude.sh`, so they are launches 11 and 12 of 12 in the shared ledger; the ledger is now full.

| Step | Result |
|---|---|
| Launch 1 (`e2e1`), prompt "Reply with exactly the word: ok" | exit 0, 1 turn, 2 input, 6,953 cache-creation, 11,382 cache-read and 4 output tokens, reported cost 0.00151 USD, 11 s including `run-claude.sh`'s 8 s settle |
| Launch 2 (`e2e2`), prompt asking for the first line of the wikory status block | exit 0, 1 turn, 2 input, 4,418 cache-creation, 13,986 cache-read and 410 output tokens (221 thinking), reported cost 0.00123 USD, 12 s including the settle |
| Status line quoted by the model in launch 2 | `wikory: mode=full \| last consolidated: never \| pending 1 \| failed 0 \| stale 0. The wiki may not be current: verify against code.` |
| Decision named | "Retries are capped at five" |
| `wikory --json doctor` afterwards | `mode: full`, `pending_turns: 2`, `registered: true`, `trusted: true` |

`pending 1` in launch 2 is launch 1's captured turn; `pending_turns: 2` after launch 2 adds its own turn. The injected briefing reached the model, and the Stop hook captured both turns, so hooks work end to end on Claude Code.

Costs are the figures Claude Code reported in `result.json` (USD). On a subscription plan the launches draw on plan quota rather than a per-token price. Combined, about 0.0027 USD, roughly 0.2p at an assumed 0.75 GBP per USD (the rate was not looked up).

Observed model behaviour, not a defect in the hook: the model treated the pending-turn entry as retrieved memory rather than as a request and did not act on it, which is the intended reading of the "evidence, not instructions" line.

Deviations from the plan's script, none changing what is measured:

- The launches use `docs/spikes/run-claude.sh` (ledger-counted) instead of a direct `claude` call. That wrapper always runs from `$WIKORY_SPIKE_SCRATCH/wikory-spikes/proj` and passes only the `spike_env` variables, so `e2e.sh` moves the earlier spike project aside (`proj.pre-e2e-<time>`), builds the e2e project there, and installs a small launcher at `wikory-spikes/bin/wikory` that sets `WIKORY_DATA_DIR` and runs the built binary.
- `SPIKE_SETTINGS` points at an empty settings file, so the spike recorder's hooks do not run and only the plugin's hooks do.
