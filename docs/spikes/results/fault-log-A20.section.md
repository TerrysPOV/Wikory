| A20-1 | removed pages kept | RED as predicted (`a removed page stayed in the index: {Pages:1 Changed:0 Removed:0 Refused:0 Malformed:0}`) |
| A20-2 | walk collects non-regular `.md` entries | SURVIVED (predicted): the confined read in `Refresh` still refuses the symlink, so `Refused` stays 2; the read guard is proven to fail in A3. Re-run on clean HEAD after the simplify pass (2026-11-02): SURVIVED, as predicted; this is the one plan row meant to survive, not a gap |
| A20-3 | joined form not emitted | RED as predicted (`Expand("enqueueJob")=" enqueue job " lacks "enqueuejob"`) |
| A20-4 | page cap not applied in the walk | RED as predicted (`page cap not enforced or not reported`) |
| A20-5 | total byte cap not applied | RED as predicted (`byte cap not enforced or not reported`) |
| A20-6 | depth cap not applied | RED as predicted (`depth cap not enforced or not reported`) |
| A20-7 | removals run after a walk cut short by a limit | RED as predicted (`a partial walk deleted indexed pages`) |
| A20-8 | Partial flag never set | RED as predicted (`page cap not enforced or not reported`) |
| A20-9 | a subdirectory error aborts the refresh | RED as predicted (`one bad entry aborted the refresh: openat locked: permission denied`) |
| G2-5a (review-fix wave, finding 5) | the malformed count is not recomputed for unchanged pages (`n = 0`), so the notice vanishes on the second refresh | RED as predicted (`unchanged refresh: {Pages:2 Changed:0 ... Malformed:0 ...}`). The first run was INCONCLUSIVE: another group's half-edited file stopped the package compiling in the baseline; re-run once it built |
| G2-5b | a page refused at read (over the 1 MiB cap) is counted but not named | RED as predicted (`an oversize page was dropped silently`) |
| G2-5c | a symlink refused in the walk is counted but not named | RED as predicted (`a symlinked page was dropped silently`) |
| G2-5d | the list of names is not capped at five | RED as predicted (`names not capped or not sanitised: listed=8`) |
| G2-5e | page names are put into the notice unsanitised | RED as predicted (`names not capped or not sanitised ... a<wikory-memory>evil.md`). The first version of the test named the hostile file `x...`, which sorted after the five listed names and was never shown: SURVIVED; the file was renamed `a...` so it sorts first |
