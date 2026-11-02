| A12-1 | epoch not checked at step 1 | SURVIVED as predicted (second guard: `recordOps` and every later step are fenced by the epoch; A12-6 proves lock and fence together) |
| A12-2 | base hash not checked at step 1 | RED as predicted (`a refused publish changed files: b="EDITED BY A HUMAN"`) |
| A12-3 | fingerprint not checked | RED as predicted (`want ErrFingerprint, got <nil>`) |
| A12-4 | checkout not checked | RED as predicted (`want ErrWrongCheckout, got <nil>`) |
| A12-5 | replay of a done job allowed | RED as predicted (`replaying a finished job: want ErrJobState`) |
| A12-6 | takeover without the publishing lock | RED as predicted (`the publish in progress was fenced mid-way: publish: lease epoch is stale`) |
| A12-7 | operations not marked in the database | RED as predicted (`db marks: 0 of 2 ops done`) |
| A12-8 | G4: `ErrConflict` message reverted to "nothing was overwritten" (false when the conflict is raised mid-apply) | RED as predicted (`the conflict message does not warn that earlier operations may be applied`) |
| A12-9 | G4: Recover does not treat a database-final job as final (`finalState(dbState)` disabled) | RED as predicted (`was not skipped cleanly`, `publish: the job is not open`) |
| A12-10 | G4: Recover returns on the first per-job error instead of collecting it | RED as predicted (`the good job was not recovered past the corrupt one`) |
| A12-11 | G4: `conflict()` discards the `setJob` error | RED as predicted (`the failure to record the conflict in the database was discarded`) |
| A12-12 | G4: `conflict()` discards the `writeManifest` error | RED as predicted (`the failure to record the conflict in the manifest was discarded`) |
| A12-13 | G4: Recover drops the failure to record a conflict (`errors.As` branch disabled) | RED as predicted (`the unrecorded conflict is not reported`) |
| A12-14 | G4: Takeover has no checkout filter (`OR 1=1`) | RED as predicted (`co-2 fenced co-1's job: err=<nil> epoch 1 -> 2`) |
| A12-15 | G4: duplicate targets accepted by revalidate | RED as predicted (`b="B1" state=conflict`) |
| A12-16 | G4: `AbandonAfter` not required by the lazy config check | RED as predicted (`AbandonAfter 0 abandoned a live job`) |
| A12-17 | G4: `Begin` does not run the lazy config check | RED as predicted (`a zero-value Publisher panicked`; the test turns the panic into a named failure) |
| A12-18 | G4: an oversize log is not rotated (`maxFile` comparison disabled) | RED as predicted (`a full log failed the publish`, `rotated=0 bytes`) |
| A12-19 | G4: the log search keeps no overlap between chunks (`carry = 0`). The first version of the test put the token after the chunk boundary and SURVIVED; the offset was fixed and the row re-run | RED as predicted (`the job's line was not found in an oversize log`) |
| A12-20 | G4: recovery discards the kept-backup list (`rep.Kept` not filled) | RED as predicted (`the kept backup is not reported`) |
| A12-21 | G4: an abandoned job's stage directory is not removed | RED as predicted (`stage directory survived recovery`) |

G4 comment-only fixes (publish.go and recover.go comments, root.go and confine.go comments, `BackupTarget`, `SetStepHook` doc, hook_test.go, deps.go) have no fault row: a comment cannot be made to fail a test.
