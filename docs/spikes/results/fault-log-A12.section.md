| A12-1 | epoch not checked at step 1 | SURVIVED as predicted (second guard: `recordOps` and every later step are fenced by the epoch; A12-6 proves lock and fence together) |
| A12-2 | base hash not checked at step 1 | RED as predicted (`a refused publish changed files: b="EDITED BY A HUMAN"`) |
| A12-3 | fingerprint not checked | RED as predicted (`want ErrFingerprint, got <nil>`) |
| A12-4 | checkout not checked | RED as predicted (`want ErrWrongCheckout, got <nil>`) |
| A12-5 | replay of a done job allowed | RED as predicted (`replaying a finished job: want ErrJobState`) |
| A12-6 | takeover without the publishing lock | RED as predicted (`the publish in progress was fenced mid-way: publish: lease epoch is stale`) |
| A12-7 | operations not marked in the database | RED as predicted (`db marks: 0 of 2 ops done`) |
