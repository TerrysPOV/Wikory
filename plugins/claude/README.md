# Wikory plugin for Claude Code (increment 1a)

1. Put the `wikory` binary on your PATH (`go install github.com/TerrysPOV/Wikory/cmd/wikory@latest`
   or a release binary), then run `wikory doctor`.
2. Install this plugin.
3. In your repository, run `wikory init` and commit `project-memory/`.

In 1a the plugin records each turn deterministically on `Stop`, marks turn starts on
`UserPromptSubmit`, and injects a short briefing on `SessionStart`. It does no model work.
Wikory never commits or pushes; review `project-memory/` changes yourself.

## Bootstrap (stage 2)

`/wikory:bootstrap` builds a labelled baseline wiki for an existing project. It needs `wikory init` done, `project-memory/` committed and a clean checkout of the default branch. Nothing is sent to a model until you say yes to the plan it shows.

What is enforced and what is asked. The command's `allowed-tools` line pre-approves five `wikory bootstrap` subcommands (`plan`, `next`, `submit`, `finish`, `status`) and the tool that starts subagents; anything else it tries still prompts, because pre-approval does not remove the session's other tools. `confirm` is left off the list on purpose: Claude Code asks you to approve each `wikory bootstrap confirm` call, and that prompt is the cost gate. If your own permission settings allow `Bash(wikory:*)` (or any rule that covers `confirm`) or you run with permissions bypassed, `confirm` is not prompted and the gate is the command's instructions alone; the command tells you so before it confirms. `wikory-extractor` has the tool list `Read` and `wikory-verifier` has `Read, Grep, Glob`, so neither has an edit, shell or network tool. `Read` is not scoped to a path, so the extractor reading only its one work file is an instruction in its prompt, backed by the binary: it keeps only claims whose excerpt is found verbatim in the work file and rejects credential-shaped text.

The extractor and verifier subagents read redacted work files that `wikory bootstrap` writes under its private store, by absolute path. If your permission mode asks before reading outside the project, allow reads under the store directory printed by `wikory doctor` for the duration of a bootstrap (an allow rule such as `Read(<store>/bootstrap/**)`). Whether subagents are prompted for these reads under default permissions is unverified (see docs/verified-capabilities.md, V-bootstrap-1); if they are, that rule is the workaround.

**Do not run a paid bootstrap on a real project until V-bootstrap-1 is recorded in docs/verified-capabilities.md.** It is a manual check that needs a person at an interactive Claude Code session, and it has not been done yet.

### Manual check V-bootstrap-1 (not yet done)

Run it in a throwaway repository, with default permissions, and write nothing into docs/verified-capabilities.md until it has been run. Observe:

1. The agent resolves as `wikory-extractor` or `wikory:wikory-extractor` (try both names when the command starts a subagent, and note which one works).
2. The tool that starts a subagent is called `Task` or `Agent` in the running build, and the command's `Task` entry in `allowed-tools` covers it.
3. A subagent's `Read` of a work file under the store (the `work_path` from `wikory bootstrap next`) is allowed, prompted or refused.
4. `wikory bootstrap submit` with the results in a quoted here-document is accepted without a prompt under the `Bash(wikory bootstrap submit:*)` pattern.
5. Confirm that a chained second command prompts: a `wikory bootstrap` call followed by any other command gets a prompt.
6. `wikory bootstrap confirm` prompts, and the command waits for your approval of it.

If a bootstrap stops part way (the session ends, a lease lapses, finish is interrupted), run `/wikory:bootstrap` again: it checks `wikory bootstrap status` first and offers to resume the latest run where it stopped. Resuming a run you already said yes to needs no new yes; a run that was only planned, or a sample waiting for the full run, still needs one. To drop a run instead, `wikory bootstrap abandon --run RUN` marks it abandoned and changes no page; then plan again.

Wikory never deletes anything it read. If you bootstrapped from an old wiki (`--source name=DIR`), the coverage report ends with a checklist: back the folder up, then delete it by hand.
