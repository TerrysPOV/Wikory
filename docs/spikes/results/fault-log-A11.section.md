# Fault log: A11 claim validator

Run with `scripts/inject.sh`, one site at a time, against the committed A11 code. Each row was predicted RED.

| # | Fault | Verdict | Predicted |
|---|---|---|---|
| A11-1 | self-evidence keeps current-impl | RED (exit 0) | RED |
| A11-2 | read-time self-evidence shown verified | RED (exit 0) | RED |
| A11-3 | uncommitted evidence verifies | RED (exit 0) | RED |
| A11-4 | hash comparison skipped | RED (exit 0) | RED |
| A11-5 | human edit ignored | RED (exit 0) | RED |
| A11-6 | committed status trusted | RED (exit 0) | RED |
| A11-7 | evidence paths not checked | RED (exit 0) | RED |
| A11-8 | reader follows symlinks | RED (exit 0) | RED |
| A11-9 | tracked-but-dirty evidence verifies | RED (exit 0) | RED |
| A11-10 | ambiguous short id guessed instead of refused | RED (exit 0) | RED |
| A11-11 | evidence confirmed by prefix | RED (exit 0) | RED |
