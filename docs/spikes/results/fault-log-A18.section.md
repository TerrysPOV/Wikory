# Fault log, A18 (capture)

| Row | Verdict | Note |
|---|---|---|
| A18-1 | RED as predicted | |
| A18-2 | RED as predicted | |
| A18-3 | RED as predicted | |
| A18-4 | RED as predicted | |
| A18-5 | RED as predicted | |
| A18-6 | RED as predicted | |
| A18-7 | RED as predicted | |
| A18-8 | RED as predicted (re-run after A19) | `TestSealedRecordRedactsCommandsAndVerification` lives in A19's seal_test.go; first run was INCONCLUSIVE (no test matched); re-run with A19 landed is RED, failure text `a secret reached the store: {` |
| A18-9 | RED as predicted | |
| A18-10 | RED as predicted | |
| A18-11 | RED as predicted | |
| A18-12 | RED as predicted | |
| G2-1a (review-fix wave, finding 1) | RED as predicted | git gets 100 times the time left instead of half, so a blocked `git status` eats the whole Stop deadline: `TestStopSurvivesGitStatusBlockedOnAFifoExclude`: `the turn was not captured` |
| G2-1b | RED as predicted | the lost git facts are reported under a different kind: `the lost git facts were not reported: [x]` |
| G2-HEAD (orchestrator item, capture.go HeadCommit error) | RED as predicted | an unresolvable HEAD stored as an empty revision (`head = ""` for `head = UnknownRevision`): `TestStopRecordsAnUnresolvableHeadAsUnknown`: `warned=[git git-status] record={` (head empty) |
| G2-HEAD-b | RED as predicted | the failure is warned under another kind (`r.Warn("gitx")`): `warned=[gitx` |
