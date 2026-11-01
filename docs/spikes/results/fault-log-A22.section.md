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
