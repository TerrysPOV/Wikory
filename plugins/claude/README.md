# Wikory plugin for Claude Code (increment 1a)

1. Put the `wikory` binary on your PATH (`go install github.com/TerrysPOV/Wikory/cmd/wikory@latest`
   or a release binary), then run `wikory doctor`.
2. Install this plugin.
3. In your repository, run `wikory init` and commit `project-memory/`.

In 1a the plugin records each turn deterministically on `Stop`, marks turn starts on
`UserPromptSubmit`, and injects a short briefing on `SessionStart`. It does no model work.
Wikory never commits or pushes; review `project-memory/` changes yourself.
