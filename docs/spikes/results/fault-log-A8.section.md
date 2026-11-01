# Fault log: A8 (internal/gitx)

Run with `scripts/inject.sh`, one site at a time, against the committed code. Each row was RED as predicted.

| # | Fault | Failing assertion seen | Verdict |
|---|---|---|---|
| A8-1 | fsmonitor override removed | `TestRepositoryFsmonitorIsNeverRun`: the repository's core.fsmonitor command ran | RED |
| A8-2 | literal pathspecs removed | `TestLiteralPathspecs`: a glob matched as a pathspec: [notes.md] <nil> | RED |
| A8-3 | sha validation removed | `TestCommitsHeadAndBranch`: an option-shaped sha must be refused, not passed to git | RED |
| A8-4 | filter and diff drivers not neutralised (override pairs never appended) | `TestDriversAreNeutralised`: missing override "filter.evil.clean"="" | RED |
| A8-5 | ambiguity not reported as ErrAmbiguous | `TestAmbiguousShortIDIsAnError`: an ambiguous prefix must be an error | RED |

## Security-review fixes (second pass)

| # | Fault | Failing assertion seen | Verdict |
|---|---|---|---|
| A8-6 | override key cut at its first `=` (what `-c key=value` did) | `TestHostileConfigKeyNamesAreNeutralised`: a driver named with '=' ran | RED |
| A8-7 | every failure of the driver-config read ignored | `TestDriverDiscoveryFailsClosed`: a corrupt config returned no error | RED |
| A8-8 | exit status 1 accepted whatever stderr says | `TestDriverDiscoveryFailsClosed`: an unreadable config returned no error | RED |
| A8-9 | exit status 1 no longer recognised as "no matching keys" | `TestDriverDiscoveryFailsClosed`: want an empty list and no error | RED |
| A8-10 | `--ignore-submodules=all` removed from status | `TestSubmoduleConfigIsNeverRun`: a filter defined in a submodule's own config ran | RED |
