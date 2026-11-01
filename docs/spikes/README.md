# Stage 0 spikes

Spikes run outside this repository, in `$WIKORY_SPIKE_SCRATCH/wikory-spikes/`. Nothing here
edits `~/.claude` or `~/.codex`, and no credential is ever copied. Raw captures stay in the scratch
directory; only files that pass `scripts/scrub-fixture.sh` are committed, under `evidence/<spike-id>/`
or `internal/decode/testdata/`. Each result goes in `results/<ID>.md`; `assemble.sh` builds
`../verified-capabilities.md`. Every launch goes through `lib.sh`, which keeps the 12-launch ledger.
