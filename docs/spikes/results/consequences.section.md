## Design consequences for increment 1a

Each line is a verified, refuted or partial result that changes a 1a task. The full row is in the table above.

- V06 (A16): the Stop payload keys are fixed by the committed `stop.json` fixture; struct tags follow it, unknown keys (`effort`, `permission_mode`, `background_tasks`, `session_crons`) are ignored, and `last_assistant_message` is present.
- V07 (A18): `prompt_id` is the turn id source (identical in UserPromptSubmit, Stop and SessionEnd, absent from SessionStart); the stored turn-start marker stays as the fallback. Verified for one turn only.
- V08 (A19): the transcript at Stop lacks the final assistant message, so take it from `last_assistant_message`, reconcile with the transcript later, and keep the sealing timeout.
- V09 (hooks, A27): REFUTED, Claude Code kills a hook's descendants, so a hook cannot leave a surviving detached child. No detached work in hooks; use lazy recovery at the next operation.
- V13 (A16): subagents share the parent's `session_id`, so self-exclusion keys on `agent_id` and `agent_type`; decode `agent_transcript_path` too. One `-p` run can hold two turns.
- V15 (A16): background subagents fire SubagentStop and the run ends cleanly in print mode; the decoder must expect `background_tasks` and a second synthetic task-notification prompt and Stop per run.
- V16 (A16, Stop hook): parallel blocking Stop hooks do not mask each other; a Wikory Stop hook must treat `stop_hook_active: true` as already continued and not block again.
- V17 (1b decoder): hook feedback appears as `type: "user"` lines with `isMeta: true`; drop them (and `api-request-blob` lines) from turn content so continuation text is not recorded as the user's words.
- V19 (A27, status line): partial, a plugin cannot supply `statusLine` (only `subagentStatusLine`), so the status line must be wrapped from the user's own settings; rendering is unobserved.
- V20 (stage 4b): REFUTED, the `autoMemoryDirectory` redirect is read back as ordinary memory, so it is not a write-only route and the `preferences[]` plus `wikory export claude-memory` fallback stands.
- V24 (1d): the deny hook is a tripwire for direct forms only (10 of 10 caught, 6 of 6 indirect forms missed); do not claim it blocks human-only commands against an evading agent.
- V25 (1b slice reader): slices must be cut by transcript line type (keep `user`, `assistant`, tool use and result; drop `attachment` and `api-request*`) and capped by turn before any budget applies.
