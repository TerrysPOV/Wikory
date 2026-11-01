# Fault log: A8 (internal/gitx)

Run with `scripts/inject.sh`, one site at a time, against the committed code. Each row was RED as predicted.

| # | Fault | Failing assertion seen | Verdict |
|---|---|---|---|
| A8-1 | fsmonitor override removed | `TestRepositoryFsmonitorIsNeverRun`: the repository's core.fsmonitor command ran | RED |
| A8-2 | literal pathspecs removed | `TestLiteralPathspecs`: a glob matched as a pathspec: [notes.md] <nil> | RED |
| A8-3 | sha validation removed | `TestCommitsHeadAndBranch`: an option-shaped sha must be refused, not passed to git | RED |
| A8-4 | filter and diff drivers not neutralised | `TestDriversAreNeutralised`: missing override "filter.evil.clean=" | RED |
| A8-5 | ambiguity not reported as ErrAmbiguous | `TestAmbiguousShortIDIsAnError`: an ambiguous prefix must be an error | RED |
