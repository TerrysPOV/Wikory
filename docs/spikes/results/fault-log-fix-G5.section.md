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
