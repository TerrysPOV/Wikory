| A13-1 | recovery overwrites unexpected content | SURVIVED as predicted (second guard: the re-hash before the rename, A13-9) |
| A13-2 | log append not idempotent | RED as predicted (`a="A1" b="B1" log=2 state=done`) |
| A13-3 | recovery ignores checkout binding | RED as predicted (`another checkout's recovery touched the job`) |
| A13-4 | temps not removed | RED as predicted (`temp or backup file left behind: .wikory-tmp-b.md-`) |
| A13-5 | jobs without a manifest never abandoned | RED as predicted (`aExists=false b="B0" log=0 state=begun rep={Completed:[] Conflicts:[]`) |
| A13-6 | an already-marked operation is trusted, not re-verified | RED as predicted (`an edit to an applied page was not reported: rep={Completed:[job_`; the plan's expected text carried a time-dependent job id prefix `job_01M6B`, which made the first run INCONCLUSIVE, so the expected text was shortened to `job_`) |
| A13-7 | recovery ignores a branch switch | RED as predicted (`recovery applied a job across a branch switch: rep={Completed:[job_`; expected text shortened for the same reason) |
| A13-8 | recovery does not take the lease over | RED as predicted (`recovery did not take the lease over: epoch 1 -> 1`) |
| A13-9 | the moved-aside page is not judged against the base hash | RED as predicted (`err=<nil> b="B1" state=done`) |
| A13-10 | a backup whose target is missing is not put back | RED as predicted (`a="A1" b="" log=0 state=conflict`) |
| A13-11 | a backup whose target already holds the new content is kept | RED as predicted (`temp or backup file left behind: .wikory-backup-b.md-`) |
| A13-12 (extra) | kill hook at `staged` never fires, so the helper runs to completion | RED (`step staged: helper was not killed (exit 0)`): proves the kill assertion can fail |
