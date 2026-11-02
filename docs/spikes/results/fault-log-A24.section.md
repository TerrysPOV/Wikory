| A24-1 | decode error propagated | RED as predicted (`{stop {PAYLOAD-CONTENT-MUST-NOT-BE-LOGGED}: code=1 out=""`) |
| A24-2 | no deadline | RED as predicted (`out=wikory: mode=full`) |
| A24-3 | the servable gate opened to any store | RED as predicted (`an unregistered checkout was served memory: wikory: mode=full \| last c`) |
| A24-4 | payload logged | RED as predicted (`relative-PAYLOAD-CONTENT-MUST-NOT-BE-LOGGED`) |
| A24-5 | synchronous discovery after the deadline | RED as predicted (`budget 300ms plus 250ms slack`) |
| A24-6 | the error log opened without `O_NONBLOCK` | RED as predicted (`the hook did not return within`) |
| A24-7 | the error log accepted when it is not a regular file | RED as predicted (`the hook wrote its error log into a FIFO`) |
| A24-8 | stdin read with no deadline | RED as predicted (`the hook did not return within`) |
| A24-9 | open failure restores the root-reading degraded briefing (security fix: status only when the store cannot be opened) | RED as predicted (`an unconfirmed checkout had repository content injected: wikory: mode=degraded`) |
| A24-10 | an unregistered checkout gets a degraded briefing that reads its `index.md` | RED as predicted (`an unregistered checkout had repository content injected`) |
| G2-1c (review-fix wave, finding 1) | UserPromptSubmit default budget set to 2 s instead of 2.5 s | RED as predicted (`kind user-prompt: budget 2s, want 2.5s`) |
| G2-1d | Stop not bounded by the hook deadline (registry.lock held: the store waits 5 s) | RED as predicted (`code=0 elapsed=5.046s: the hook outlived its 400ms budget`) |
| G2-1e | UserPromptSubmit not bounded by the hook deadline | RED as predicted (`elapsed=5.045s: the hook outlived its 400ms budget`) |
| G2-2a (finding 2) | UserPromptSubmit does not log an open failure | RED as predicted (`a corrupt config.toml was not logged by both hooks: "... open\n"`) |
| G2-2b | Stop does not log an open failure | RED as predicted (same text) |
| G2-2c | a repository without project-memory is no longer silent | RED as predicted (`a repository without Wikory logged hook errors`) |
| G2-4a (finding 4) | SessionStart discards the Seal error | RED as predicted (`the seal failure was not logged: ""`) |
| G2-4b | UserPromptSubmit discards the Seal error | RED as predicted (`UserPromptSubmit did not log its seal failure`) |
| G2-4c | SessionStart discards the SweepStale error | RED as predicted (`the sweep failure was not logged: ""`) |
| G2-10 (finding 10) | a failed write of the briefing to stdout is not logged | RED as predicted (`the failed write was not logged: ""`) |
| G2-3b (finding 3) | the 24-hour window removed from the hook error count | RED as predicted (`hook_errors=3`) |
| G2-3c | the log is never trimmed | RED as predicted (`size=2097199 ... the log was not capped to its newest part`) |
| G2-3d | the trimmed log does not start at a line boundary | RED as predicted (`the trimmed log starts mid-line`) |
| G2-3e | an unwritable log not reported by doctor | RED as predicted (`an unusable hook error log went unreported`) |
| G2-3f | the Lstat regular-file check removed from `readHookLog` | SURVIVED as predicted: a second guard, `openHookLogRead` (O_NOFOLLOW, O_NONBLOCK, fstat), refuses a symlink and a FIFO and the same notice is raised. The Lstat check stays because it names the kind of file in the message and covers Windows, where links are followed |
