# Fault log: A16 (Claude Code hook payload decoder)

Run with `scripts/inject.sh` on 2026-11-01. All five rows gave RED, as predicted. The optional `session-start-clear.json` fixture is absent, which does not affect these rows.

| # | Fault | Verdict | Failure text seen |
|---|---|---|---|
| A16-1 | event name not checked | RED | `wrong event name: want ErrMalformed, got <nil>` |
| A16-2 | size bound off by the whole limit | RED | `oversized: want a size error, got ... unexpected EOF` |
| A16-3 | stop_hook_active tag wrong | RED | `continuation fixture should have stop_hook_active true` |
| A16-4 | prompt_id tag wrong | RED | `prompt_id ups="" stop=""` |
| A16-5 | agent_transcript_path not decoded | RED | `subagent stop should carry agent_id, agent_type, agent_transcript_path ...` |
