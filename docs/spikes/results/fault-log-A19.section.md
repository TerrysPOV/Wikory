# Fault log, A19 (sealing)

| Row | Verdict | Note |
|---|---|---|
| A19-1 | RED as predicted | |
| A19-2 | RED as predicted | |
| A19-3 | RED as predicted | |
| A19-4 | RED as predicted | |
| A19-5 | RED as predicted | |
| A19-6 | RED as predicted | |
| A19-7 | RED as predicted | |
| A19-8 | RED as predicted | |
| A19-9 | RED as predicted | |
| A18-8 (re-run after A19) | RED as predicted | `TestSealedRecordRedactsCommandsAndVerification`: `a secret reached the store: {` |
| G2-4 (review-fix wave, finding 4) | RED as predicted | Seal returns on its first error instead of continuing (`return sealed, err` for the count-and-continue block): `TestSealContinuesPastACorruptOldestTurn`: `the failure was not reported with its count: n=0 err=invalid character` |
| G2-9a | RED as predicted | the oversize test: the oversize flag needs 4 times the limit: `TestSealedIncompleteRecordsSayWhy`: `want reason oversize` |
| G2-9b | RED as predicted | an outside-root transcript is labelled `unreadable`: `want reason outside-root` |
