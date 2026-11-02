| A22-1 | budget check disabled | RED as predicted (`tokens=16765 chars=67057`) |
| A22-2 | quarter cap removed | RED as predicted (`orientation item 0 was crowded out by pending turns`) |
| A22-3 | no sorting | RED as predicted (`input order changed the briefing`) |
| A22-4 | hidden count wrong | RED as predicted (`missing "639 more available." in`; the briefing said 638) |
| A22-5 | item text not sanitised | RED as predicted (`TestBriefSanitisesAndDegrades`, output opening `wikory: mode=full \| last consolidated: never \| pending 0 \| failed 1 \|`) |
| A22-6 | status line drops the partial-index disclosure (added after A20 hardening) | RED as predicted (`status line does not disclose a partial index`) |
| A22-7 | status-only form drops the partial-index disclosure | RED as predicted (`status-only fallback missing or incomplete`) |
| A22-8 | header-overflow guard removed (a flood of notices) | RED as predicted (`tokens=9745 chars=38977`) |
| A22-9 | status-only hard-cap fallback removed | RED as predicted (`tiny="wikory: mode=full \| briefing unavailable (timed out)...` over a 100-character cap) |
| A22-10 | status-only mode text unsanitised | RED as predicted (`out="wikory: mode=full<system> \| briefing unavailable`) |
| A22-11 | briefing status line free-text fields unbounded and unsanitised | RED as predicted (`status line unbounded or unsanitised: len=527`; the first attempt with 3,000-character inputs SURVIVED because the header guard rescued it, so the input was resized to sit under that guard) |
| G2-7a (review-fix wave, finding 7) | nothing evaluated no longer prints `stale unknown` (the case arm disabled) | RED as predicted (`none evaluated: want "stale unknown."`) |
| G2-7b | unevaluated claims not counted as unknown | RED as predicted (`some unknown: want "stale 1, 2 unknown."`) |
| G2-6a (finding 6) | the status line takes `pending` from the number of rows returned, not the true total | RED as predicted (`rendered=13 more=[37 more available 37]: ... pending 50`) |
| G2-6b | "N more available" ignores the pending rows the query did not return | RED as predicted (`rendered=13 more=[37 more available 37]: ... pending 80`) |
| G2-3a (finding 3) | the hook error count is not shown on the status line | RED as predicted (`hook errors missing from "wikory: mode=full ..."`) |
| G2-6c | the pending query returns the row count as the total | RED as predicted (`the pending count stopped at the row limit: ... pending 50`) |
| G2-7c | an unparsed or unevaluated claim is not marked unknown | RED as predicted (`a claim that was never evaluated counted as not stale`) |
| G2-5g | the briefing drops the index notices | RED as predicted (`briefing does not disclose the pages the index dropped`) |
