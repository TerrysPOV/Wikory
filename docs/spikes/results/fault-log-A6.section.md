## Fault log: Task A6

| # | Fault | Predicted failure | Verdict |
|---|---|---|---|
| A6-1 | a shared lock instead of an exclusive one | `TestLockExcludesASecondHolder`: `second holder: want ErrLockTimeout, got <nil>` | RED as predicted |
