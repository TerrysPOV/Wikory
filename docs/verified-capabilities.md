# Verified capabilities (stage 0)

Products: Claude Code 2.1.316, Codex CLI 0.165.0, Go 1.27.3 (go directive 1.26.0), macOS arm64 host.
Status values: verified, refuted, partial, pending-ci, not-verifiable-here.
Assembled by docs/spikes/assemble.sh from docs/spikes/results/. Do not edit by hand.

| ID | Item (spec 14.1) | Method | Result | Evidence | Design consequence | Status |
|---|---|---|---|---|---|---|
| V01 | FTS5 in modernc.org/sqlite | S1 | | | | pending |
| V02 | Minimum Go version for rooted opens; os.Root behaviour (unix) | S2 | | | | pending |
| V03 | os.Root and reparse points on Windows, junctions | S2 (CI) | | | | pending-ci |
| V04 | SQLite WAL under parallel processes (macOS) | S3 | | | | pending |
| V05 | SQLite WAL under parallel processes (Windows) | S3 (CI) | | | | pending-ci |
| V06 | Exact Stop payload fields on 2.1.316 | S5 | | | | pending |
| V07 | `prompt_id` present and equal in UserPromptSubmit and Stop | S5 | | | | pending |
| V08 | Transcript lag at Stop | S5 | | | | pending |
| V09 | Hook can detach a child that survives the hook timeout | S5 | | | | pending |
| V10 | Bash tool gives processes a pty | S6 | | | | pending |
| V11 | `tools` allowlist enforced for plugin subagents | S7 | | | | pending |
| V12 | Plugin MCP tools visible to tools-allowlisted subagents | S7 | | | | pending |
| V13 | Subagents share the parent's session_id | S7 | | | | pending |
| V14 | Plugin hooks in exec form; plugin-bundled binary viable | S7 | | | | pending |
| V15 | `background` subagents fire SubagentStop; continuation ends cleanly | S8 | | | | pending |
| V16 | How several blocking Stop hooks combine | S9 | | | | pending |
| V17 | How hook-injected continuation messages appear in the transcript | S9 | | | | pending |
| V18 | `/clear` fires SessionStart with source `clear` | S10 | | | | pending |
| V19 | A plugin can supply or wrap `statusLine` | S10 | | | | pending |
| V20 | `autoMemoryDirectory` redirect gives write-only memory; memory file format | S11 | | | | pending |
| V21 | Codex Stop can force a continuation | S12 | | | | pending |
| V22 | Codex agent definitions enforce tool restrictions | S12 | | | | pending |
| V23 | Codex sandbox blocks a `wikory` call writing to the private store | S12 | | | | pending |
| V24 | PreToolUse deny hook vs shell indirection | S13 | | | | pending |
| V25 | MCP-only subagents can read a transcript slice within a workable budget | S13 | | | | pending |
| V26 | Plugin packaging and trust behaviour | S13 | | | | pending |
| V27 | Pilot repository is private | S13 | | | | pending |
| V28 | Orca workers rebuild generated settings each launch; allowlisted plugin loads in a worker | S13 | | | | pending |
| V29 | User-typed shell shortcut behaviour per harness | S10 / S13 | | | | pending |
| V30 | ChatGPT Desktop supports local stdio MCP | none | | | | not-verifiable-here |
| V31 | Obsidian reading view hides claim comments | none | | | | not-verifiable-here |
| V32 | `wikory.com` ownership | none | | | | not-verifiable-here |
