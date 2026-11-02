| G5-1a | `abandonStale` abandons every begun job regardless of age: `p.Now().Sub(t) >= p.AbandonAfter` becomes `... \|\| true` | RED as predicted (`a fresh job was abandoned`) |
| G5-1b | an unparseable `created_at` reads as the zero time and so as ancient: `if perr != nil` becomes `if false` | RED as predicted (`a job with an unreadable age was abandoned`) |
| G5-2 | the stamp command drops the CRLF it should restore on a rewritten claim line: `if cr {` becomes `if cr && false {` | RED as predicted (`line 3 lost its CRLF (mixed endings)`) |
| G5-3a | the `github_pat_` alternative removed from the github-token rule | RED as predicted (`fine-grained token survived`) |
| G5-3b | the api-key minimum body length `{20,}` becomes `{32,}` | RED as predicted (`20 characters: out="use sk-kkkkkkkkkkkkkkkkkkkk end"`) |
| G5-4a | DEL (0x7f) no longer removed by the sanitiser | RED as predicted (`DEL: "a\x7fb\x7fc", want "abc"`) |
| G5-4b | the C1 range 0x80 to 0x9f (CSI U+009B, NEL U+0085) no longer removed | RED as predicted (`CSI: "a\u009bb\u009bc", want "abc"`) |
| G5-4c | NFKC replaced by NFC in `normalise` (existing test, confirmed to still bite) | RED as predicted (`&lt;ｓｙｓｔｅｍ&gt;obey`) |
| G5-5a | `maxFinal` raised 100 times (4000 to 400000) | RED as predicted (`final message: stored 12000 runes, want 4012`) |
| G5-5b | `maxCommand` raised 100 times (2000 to 200000) | RED as predicted (`command: stored 6000 runes, want 2012`) |
| G5-5c | `maxPath` raised 100 times (1000 to 100000) | RED as predicted (`file touched: stored 3000 runes, want 1012`) |
| G5-5d | `maxDirty` raised 100 times (50 to 5000) | RED as predicted (`stored 120 dirty files, want 50`) |
| G5-6 | the fingerprint refusal no longer marks the job refused (the `setJob(..., "refused", "fingerprint changed")` line dropped) | RED as predicted (`job state "begun" after a fingerprint refusal`) |
| G5-7a | the `Seal` deadline check disabled (`if false \|\| ctx.Err() != nil`) | RED as predicted (`Seal past its deadline: n=1`) |
| G5-7b | `Seal` reads the wall clock again (`time.Now().After(deadline)`) instead of `r.Now()` | RED as predicted (`Seal past its deadline: n=1`; the whole seal suite also goes red, because its deadlines are on the recorder clock) |
| G5-8a | `thMatches` accepts a one-character prefix (`n >= 6` becomes `n >= 1`) | RED as predicted (`a 1 character prefix "e" matched`) |
| G5-8b | `thMatches` minimum moved to 5, then to 7 | RED as predicted both ways (`a 5 character prefix "ed5ac" matched`; `a 6 character prefix "ed5ac7" did not match`) |
| G5-9a | index byte cap `>` becomes `>=` | RED as predicted (`want 2 0 false`: pages summing to exactly the cap are refused) |
| G5-9b | index byte cap `>` becomes `> maxBytes+1` | RED as predicted (`want 1 1 true`: a page one byte over the cap is admitted) |
| G5-9c | briefing `fits` `<=` becomes `<` | RED as predicted (`at the exact cap 330: item shown=false`) |
| G5-9d | briefing `fits` `<=` becomes `<= cap+1` | RED as predicted (`one byte under the exact cap 329: item shown=true`) |
| G5-10a | `Takeover` no longer takes the publishing lock (now a handshake test, not a 300 ms sleep against a 200 ms floor) | RED as predicted (`takeover returned while the publish still held the lock`) |
| G5-10b | the SessionStart timeout waits an extra 2 s before giving up (the two hook budget tests now scale by `WIKORY_LATENCY_SLACK`, skip the strict budget under `-race`, and wait for the abandoned work by handshake) | RED as predicted (`elapsed=902ms out=`) |
| G5-11a | the gitx hostile-config tests (clean filter, key names, submodule, fsmonitor) now run on every platform using the test binary as the marker command; driver neutralisation dropped in `Repo.git` (`run(ctx, r.Top, nil, ...)`) | RED as predicted (`the repository's clean filter command ran`; `a driver named with '=' ran`). Found while doing this: `TestRepositoryCleanFilterIsNeverRun` SURVIVED the same fault before the fix, because its working copy differed in size so git never needed the filter; it now uses same-size content and a later mtime |
| G5-11a2 | `core.fsmonitor=false` dropped from the git safety flags | RED as predicted (`the repository's core.fsmonitor command ran`) |
| G5-11a3 | `--ignore-submodules=all` dropped from `status` | RED as predicted (`a filter defined in a submodule's own config ran`). Dropping `submodule.recurse`/`diff.ignoreSubmodules` from `baseConfig` SURVIVES: the `--ignore-submodules=all` flag is what stops the descent |
| G5-11b1 | `LPT9` removed from the reserved device names | RED as predicted (`"LPT9" (a reserved device name) was accepted`) |
| G5-11b2 | the trailing-space refusal removed | RED as predicted (`"trailing " (trailing dot or space) was accepted`) |
| G5-11b3 | the colon dropped from the forbidden characters | RED as predicted (`"f.txt:stream" (stream or forbidden character) was accepted`) |
| G5-11b4 | the case fold (`strings.ToUpper`) removed | RED as predicted (`"con" (a reserved device name) was accepted`) |
| G5-11c | Windows hard-link tests (read, append, replace) added to `confine_windows_test.go`; the `NumberOfLinks > 1` check cannot be faulted from this machine | pending-ci: compiled and vetted with `GOOS=windows`, not run |
