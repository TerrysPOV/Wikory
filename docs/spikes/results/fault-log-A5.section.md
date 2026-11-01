## Fault log: Task A5

| # | Fault | Predicted failure | Verdict |
|---|---|---|---|
| A5-1 | wider existing mode not tightened | `TestEnsurePrivateDirTightensWiderMode`: `mode=-rwxr-xr-x` | RED as predicted |
| A5-2 | symlink accepted | `TestEnsurePrivateDirRefusesSymlink`: `a symlinked store directory must be refused` | RED as predicted |
| A5-3 | port kept in the host | `TestCanonicalOrigin`: `"ssh://git@github.com:22/TerrysPOV/Wikory"` yields `github.com:22/terryspov/wikory` | RED as predicted |
