# Spike isolation (Task S4)

Check: `claude auth status` under `env -i` with `CLAUDE_CONFIG_DIR` pointing at an empty scratch directory. No credential was copied, no prompt was sent, no model call was made, and the ledger was not touched.

Verdict: a scratch `CLAUDE_CONFIG_DIR` does **not** authenticate. Not verified whether a human login inside the scratch directory would work: that needs a human login, which the harness does not attempt.

Consequence (the plan's fallback): launches use the real configuration directory. Session logs for the scratch project land in `~/.claude/projects/<slug of the scratch path>/`, and `spike_relocate_transcripts` in `docs/spikes/lib.sh` moves exactly that directory into `$WIKORY_SPIKE_SCRATCH/wikory-spikes/relocated-transcripts/` after each launch. Settings files are never edited. The relocation slug (`sed 's#[/.]#-#g'` over the scratch project path) is unconfirmed until S5 shows which directory Claude Code actually creates.

Plugin validator (V26 evidence): `claude plugin validate` on the scratch spike plugin printed `Validation passed`. It also listed directory-submission findings that do not change the result: README missing, LICENSE missing (both refused by the plugin directory), and no icon (warning).
