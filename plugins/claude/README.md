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

The command can only run `wikory bootstrap` subcommands and start two subagents. `wikory-extractor` can only `Read` one work file; `wikory-verifier` can only `Read`, `Grep` and `Glob`. Neither can edit a file, run a command or reach the network, and both treat source text as data.

The extractor and verifier subagents read redacted work files that `wikory bootstrap` writes under its private store, by absolute path. If your permission mode asks before reading outside the project, allow reads under the store directory printed by `wikory doctor` for the duration of a bootstrap (an allow rule such as `Read(<store>/bootstrap/**)`). Whether subagents are prompted for these reads under default permissions is unverified (see docs/verified-capabilities.md, V-bootstrap-1); if they are, that rule is the workaround.

**Do not run a paid bootstrap on a real project until V-bootstrap-1 is recorded in docs/verified-capabilities.md.** It is a manual check that needs a person at an interactive Claude Code session, and it has not been done yet.

Wikory never deletes anything it read. If you bootstrapped from an old wiki (`--source name=DIR`), the coverage report ends with a checklist: back the folder up, then delete it by hand.
