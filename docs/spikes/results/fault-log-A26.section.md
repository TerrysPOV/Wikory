| A26-1 | unknown versions treated as known | RED as predicted (`{Version:test Executable`) |
| A26-2 | uncommitted changes not reported | RED as predicted (`uncommitted wiki change not reported: []`) |
| A26-3 | transcript roots not reported | RED as predicted (`[]`) |
| A26-4 | hook errors not counted (added row) | RED as predicted (`hook_errors=0 index_partial=`) |
| A26-5 | index partial flag not set (added row) | RED as predicted (`hook_errors=2 index_partial=<nil>`) |
| A26-6 | recover drops the refused list (added row) | RED as predicted (`code=0 out=`) |
| G2-8a (review-fix wave, finding 8) | the briefing's mode ignores the probed version | RED as predicted (`claude "9.9.9": want mode=reduced: wikory: mode=full`). The first attempt did not compile (unused variable) and was INCONCLUSIVE; the fault was rewritten to keep the variable in use |
| G2-8b | the probed mode is not cached for the process | RED as predicted (`the probe ran 2 times in one process, want once`) |
| G2-9a (finding 9) | doctor drops the notice for a failed count query | RED as predicted (`doctor read a failing query as all clear`; the first run SURVIVED because the stuck-jobs notice also said `could not count`, so the test was tightened to require both messages) |
| G2-9b | doctor drops the notice for a failed stuck-jobs query | RED as predicted (same text) |
| G2-9c | incomplete sealed turns not reported | RED as predicted (`captured=1 sealed=2 incomplete=0`) |
| G2-9d | stuck-job age raised 1000-fold | RED as predicted (`a stuck publish job went unreported: stuck=0`) |
| G2-9e | stuck-job age zero | RED as predicted (`a job begun a moment ago was called stuck`) |
| G2-9f | a stuck job left out of the briefing's failed count | RED as predicted (`a stuck job is not in the briefing's failed count`) |
| G2-5f (finding 5) | doctor drops the index notices | RED as predicted (`doctor does not disclose the pages the index dropped`) |
| G2-9g | a failing `git status` in doctor swallowed | RED as predicted (`a failed git status was read as no uncommitted changes`) |
