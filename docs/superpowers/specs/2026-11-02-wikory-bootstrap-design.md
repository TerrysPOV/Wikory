# Wikory: stage 2 bootstrap design

Date: 2026-11-02
Status: draft 2. Subsystem design under section 9 and section 12 (stage 2) of [the architecture design](2026-11-01-wikory-architecture-design.md), which is approved and wins where the two differ. Terry ruled on the four open choices on 2026-11-02 (section 5). The design itself still awaits his review before the implementation plan is written.

## 1. Problem

An existing project has knowledge in code, docs, handoffs and memory notes but no Wikory wiki. Bootstrap builds a labelled current-state baseline from those sources. It must not invent history, quote secrets, or let a source instruct the agent. The binary does no model work, so the model-driven stages run in subagents that the working agent dispatches in batches.

## 2. Current state (read from the code at `b85a13d`)

- `store`: `modernc.org/sqlite`, one write connection, all writes under `Store.WithWriteLock`. Schema is `CREATE ... IF NOT EXISTS` with `SchemaVersion = 1`; an existing DB missing an object is repaired by re-running the schema, so additive tables need no migration step. Tables `jobs`/`job_ops` cover one publish only. There are no run, source or checkpoint tables.
- `validate.Authoring(ctx, claim, text, env, mint)` mints the ID, `th` and scope, resolves evidence paths to full OIDs, and downgrades uncommitted, dirty or turn-only evidence. It has no excerpt check and no `conflicting`/`historical` handling.
- `publish.Publisher` (`Begin`, `Publish(ops)`, `Recover`) is the only write path for repo files. `Op.OldHash` stops it overwriting a human edit. Files cap at 4 MiB; a run can be split into several jobs.
- `retrieve.Indexer.Refresh` indexes `project-memory/wiki` on the next `search`/`context`, with caps of 5000 pages and 1 MiB per page. A baseline page needs no separate reindex.
- `gitx` has no tree listing, blob-at-revision read, log or churn, or branch and worktree listing. `redact.String` replaces secret spans but returns no offsets.
- `app`: commands register via `register(Command{...})`; subcommands are hand-dispatched on `args[0]` (as `page stamp` does); `openProject` plus `servable(p)` refuses untrusted or unregistered checkouts. `NewProgress` exists but has no TTY detection (`search` hardcodes `tty=false`), no remaining-time estimate and no stage names.
- `init` records the project as trusted only when it created `project-memory/` itself. A pre-existing `project-memory/` records `TrustUndecided`, and there is no `wikory trust` command until stage 1d.
- `plugins/claude` has hooks only: no `commands/`, `agents/` or MCP. V11 (a `tools` allowlist is enforced for plugin subagents) and V12 (plugin MCP tools are visible to allowlisted subagents) are each verified from one observation. V09: a hook cannot leave a surviving child process.

## 3. Target behaviour

```
wikory bootstrap plan    [--source NAME=DIR ...] [--sample N] [--json]
wikory bootstrap next    --run R [--max N] [--json]
wikory bootstrap submit  --run R --batch B [--json]       (proposals on stdin)
wikory bootstrap confirm --run R --plan-hash H
wikory bootstrap finish  --run R [--json]
wikory bootstrap status  [--run R] [--json]
```

`plan` is deterministic and free: stages 1 to 3 of architecture section 9. `next` hands out batches of work items; `submit` ingests the subagents' structured proposals; `finish` renders pages and the coverage report (stages 6 and 7). `/wikory:bootstrap` is the in-session driver that loops `next`, dispatches subagents and pipes their results to `submit`. The cost gate is `plan`, then a measured `--sample N` run, then `confirm`.

## 4. Design

### 4.1 Run state

New private-store tables, all per checkout and added to `perCheckoutTables`:

| Table | Holds |
|---|---|
| `bs_runs` | run id, checkout id, state (`planned`, `sampling`, `awaiting-confirm`, `running`, `finishing`, `done`, `abandoned`), plan hash, scope, HEAD commit, end marker, token and duration totals |
| `bs_sources` | run id, kind, locator, content hash, revision, tier, size, disposition (`queued`, `read`, `skipped:<reason>`), secret-span count |
| `bs_components` | run id, name, path prefix, score, rank, inspected flag |
| `bs_items` | run id, stage (`extract`, `verify`), source or component ref, input hash, state (`pending`, `leased`, `done`, `rejected`), lease epoch, attempts |
| `bs_claims` | run id, item id, minted claim id, text, kind, status, evidence JSON, excerpt reference, tier |

A source is re-processed only when its content hash changed (idempotent re-runs). Items carry lease epochs, so a crashed driver's batch is re-leased and a late `submit` for an old epoch is refused.

### 4.2 Inventory, registration, prioritisation (deterministic)

- A `SourceReader` interface (`Kind`, `List`, `Read`) with a repo-tree reader (tracked files at HEAD, plus opt-in untracked locators such as `.planning/` and handoffs, read through `confine.Root`) and a directory reader for opted-in outside roots (Claude `memory/`, an existing wiki). Outside roots are never implied.
- Exclusions apply first: binary, generated, vendored, over-size, ignored by git. Secret scanning runs next. A source or span with a secret is skipped and counted, never quoted. This needs a new `redact.Scan(s) []Span` that returns offsets, because `String` cannot tell the caller what was skipped.
- Tiers: code, configuration and tests establish current implementation; decision records establish intent; handoffs, memory notes and agent summaries are attributed claims.
- Components: one per Go package, `package.json` or top-level directory, whichever the manifests give. Score is a weighted sum of 90-day churn, references from docs, mentions in handoff open items, and entry points. The weights are initial constants recorded in the plan and set properly from the sample (architecture section 14.2). Components beyond the budget are listed as not inspected.

### 4.3 Extract and verify (model work, in subagents)

- Extraction runs on redacted work files. The binary writes each item's input to `<store>/bootstrap/<run>/items/<id>.txt` (0600, confined) and the `wikory-extractor` agent has `Read` only. This keeps secrets out of the transcript and source text out of the working agent's context.
- A proposal is `{item, claims: [{text, kind, excerpt, locator}], usage}`. `submit` rejects, counts and reports any claim whose excerpt does not appear verbatim (canonicalised) in the item's work file, whose locator escapes the item, or that fails the validator.
- Extracted claims enter through `validate.Authoring` and are capped at `unverified` (or `historical` for handoff and memory tiers). A source cannot raise its own status, and instructions inside a source are only ever text.
- Verification runs per prioritised component with a `wikory-verifier` agent (`Read`, `Grep`, `Glob` on the repo). It returns confirmed, contradicted or not-determinable with evidence locators. The binary resolves each locator to a blob OID and accepts only code, configuration or test paths. Confirmed becomes `verified` through `Authoring`. Contradicted keeps the original claim and adds the code-evidenced claim, both `conflicting` with `contra` links and no silent winner. Not-determinable stays `unverified`.
- Token usage comes from the driver and is labelled asserted. The binary cannot measure it.

### 4.4 Baseline pages (deterministic)

The binary renders pages from accepted claims with templates and no model prose: `bootstrap/overview`, `bootstrap/architecture`, one `component` page per inspected component, plus constraints, known issues and unfinished work. Where rationale is unavailable the page says so. Pages go through `Publisher` with `OldHash`, in several jobs if needed, so a human-edited page is reported as a conflict and never overwritten. Pages stay under the 64 KiB claim-parsing cap by splitting per component.

### 4.5 Cost gate

`plan` prints stage-by-stage token estimates (bytes divided by four until the sample calibrates it) and the batch count, plus a currency figure only where a pricing table is configured in user config. On a subscription plan it says there is no per-token price. `--sample N` runs the real pipeline on N sources and reports measured duration, asserted tokens and the excerpt-acceptance rate. `confirm` requires the plan hash, and the full run refuses to start without it. The hash gate is advisory against a hostile local agent, in line with architecture section 14.4, and `/wikory:bootstrap` instructs the agent to pass it only after the person says yes.

### 4.6 Coverage report and end marker

Sources read and skipped with reasons, components verified, conflicting or unknown, claim counts by kind and status, rejected-proposal counts, tokens (asserted), duration, and the retirement checklist for outside sources. `--json` gives the same data. The end marker is the HEAD commit and a timestamp; real turn capture starts from it. `turns/` is untouched.

### 4.7 Progress

Fix `NewProgress` so TTY is detected from the `os.File` mode (no new dependency), add stage names and a remaining-time estimate labelled as an estimate until measured. `plan`, `next` and `finish` use the architecture section 9 notice. In the agent session `next` returns a one-line status per batch for the agent to mirror on its task list.

## 5. Rulings (Terry, 2026-11-02)

1. **Origin labelling: agreed as recommended.** An `origin` key in page frontmatter only. Claims inherit it from their page, `bs_claims` keeps the per-claim mapping privately, and the claim grammar does not change.
2. **Pilot trust: no `wikory trust grant` in stage 2.** Terry's ruling is to back up the pilot's existing wiki and then delete it. The pilot's "existing wiki" is `.nimbalyst/` in `claudeclaw-ultra` (7 files, 28 KB, mostly trackers and `local-wiki.json`), not `project-memory/`. So `init` finds no `project-memory/`, creates the store and records the project as trusted, and no trust command is needed before stage 1d.
   - **Sequencing.** The Nimbalyst folder is an opt-in bootstrap source (section 4.2), so it is backed up first and deleted only after the bootstrap run has read it. Wikory never deletes retired sources; it prints the retirement checklist and Terry deletes by hand. The backup and the delete are Terry's call at pilot time. Nothing is deleted before then.
   - **Pilot checkout.** `claudeclaw-ultra` is on `fix/wave2` with uncommitted changes. Bootstrap baselines at default-branch `HEAD` (architecture section 9), so the pilot run is done from a clean checkout of the default branch.
3. **Source text routing: agreed as recommended.** Subagents read redacted work files on disk, not text pasted into prompts.
4. **Pilot spend: noted.** Planning is free. The sample and full run are paid, and neither runs without Terry's cost go-ahead and the token maths in front of him first.

## 6. Out of scope

Session transcripts (`--include-transcripts` stays sampled-only and is not built in stage 2), the headless worker (8.6) as a driver, MCP, `wikory trust` (stage 1d), the Codex adapter, time-travel bootstrap for the benchmark, retention and backup.

## 7. Key files

New: `internal/bootstrap/` (inventory, sources, tiers, components, rank, estimate, run, items, proposal, verify, render, coverage), `internal/app/bootstrap_cmd.go`, `plugins/claude/commands/bootstrap.md`, `plugins/claude/agents/wikory-extractor.md`, `plugins/claude/agents/wikory-verifier.md`.
Modified: `internal/store/schema.go` and `clone.go`, `internal/gitx/` (tree, blob-at-rev, churn, branches, worktrees), `internal/redact/` (`Scan`), `internal/claim/page.go` (`origin` key), `internal/app/progress.go`, `scripts/exit-checks.sh`. `internal/deps/deps.go` only if a module is added (none expected).

## 8. Task outline for the plan

1. Schema tables and clone purge. 2. `gitx` tree, blob-at-rev, churn, worktrees. 3. `redact.Scan` with offsets. 4. `SourceReader`, exclusions, tiers, source registration. 5. Components and ranking. 6. Estimator, plan, plan hash and `bootstrap plan`. 7. Progress fixes. 8. Run state, leases, `next`, `status`, resume. 9. Work files. 10. Proposal schema, excerpt check, `submit` (extract). 11. Verify stage and `conflicting` handling. 12. `origin` frontmatter and deterministic page rendering through `Publisher`. 13. Coverage report, `finish`, end marker. 14. Cost gate: sample and `confirm`. 15. Plugin command and agents, with a prompt-injection fixture. 16. Exit checks, fault injection, kill-at-step tests, Windows CI, and a free `plan` dry run on this repository.

Every task is test-first, each assertion is shown to fail before it counts (`scripts/inject.sh`), and the new tests are added to `scripts/exit-checks.sh` by name.
