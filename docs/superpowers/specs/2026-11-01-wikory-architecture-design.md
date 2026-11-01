# Wikory: architecture design

Date: 2026-11-01
Status: draft 3 for review, not yet approved. Incorporates two independent reviews (Appendices C and D).
Source brief: [Project-Wiki-Memory-Proposal.md](../../Project-Wiki-Memory-Proposal.md) (v0.1, 31 October 2026), used as a discovery brief rather than a list of instructions.

## 1. Purpose and scope

Wikory gives coding agents durable, evidence-backed memory for each software project. Knowledge accumulates as a committed project wiki plus a private record of what happened in each agent turn. A fresh agent session starts with a short, honest briefing and can search the wiki on demand. The goal is a project that becomes easier for a new agent to understand over time. It is not to produce more documentation or to minimise code inspection at any cost.

This document is the programme architecture. It fixes the shape of four subsystems (engine and storage, harness adapters, bootstrap, benchmark) at component level, records the decisions that have been frozen, and lists what is still open. Each subsystem gets its own implementable specification and plan later. This is not an implementation plan.

Operating principle, carried from the proposal: consult memory to orient the work, then verify what matters against current code.

### 1.1 Goals

1. Preserve what is hard to recover from code alone: rationale, constraints, failed approaches, unfinished work and the reasons behind decisions.
2. Keep every durable claim attributable to evidence, with uncertainty, contradiction and supersession preserved rather than flattened.
3. Work for Claude Code first and Codex second, with a minimal contract for other harnesses, from a single binary with no runtime dependencies.
4. Be safe to adopt in a team and safe to open in a repository you do not control.
5. Be measurable: the benefit must be demonstrated against realistic baselines, including processing overhead, before it is claimed.

### 1.2 Non-goals

- Replacing code navigation or code graphs. CodeGraph is a benchmark condition, and later an optional provider (section 7.3).
- Documenting every function. Bootstrap prioritises active and important areas.
- Cross-project or personal memory. A separate personal memory system remains responsible for that.
- A hosted service, network sync or telemetry. The binary opens no network connections itself and sends no usage data. It may invoke `git` and, where present and consented to, `gh` for provenance.
- Non-git projects in v1.

### 1.3 Terms

| Term | Meaning |
|---|---|
| Turn | A user request plus the agent activity until completion or interruption. Tool calls and hook continuations do not create separate turns. Sub-agent work attaches to its parent turn. |
| Claim | The unit of memory: one statement with kind, status, scope and evidence. |
| Page | A topic-level markdown view over claims, one topic per file. |
| Source | A reference to material that supports a claim (code, document, commit, test result). Sources are references, never copies. |
| Wiki | The committed set of pages under `project-memory/wiki/`. |
| Private store | Per-machine data (turn records, queue, index database) that is not committed. |
| Consolidation | Updating affected wiki pages from validated turn records. |
| Promotion | Explicitly publishing a private turn into the committed `turns/` directory. |

## 2. Decision principles

1. **Deterministic work lives in the binary.** Capture, hashing, scope filtering, search, link graph, validation, queueing and publishing are code, not model behaviour.
2. **Model work happens only in subagents the harness spawns**, briefed by the binary, using the cheapest adequate model. Their output is validated before anything is published. The working agent never does memory work itself beyond dispatching it.
3. **There is a deterministic floor.** Anything that depends on a model complying with an instruction is best-effort. A turn is always recorded deterministically even if no model enrichment happens.
4. **Retrieved memory is evidence, not instructions.** It is always framed that way and never trusted blindly.
5. **Never overclaim currency.** Every briefing states pending, failed and stale counts and says plainly when the wiki may not be current.
6. **Local checks are advisory.** Real boundaries (push protection, branch protection, code review) are server-side. Local checks give early warning.
7. **Measure before building more.** Later capability is gated on benchmark evidence, with thresholds fixed beforehand.

## 3. System shape

```
Harness plugins
  claude-plugin/   hooks.json -> `wikory hook <event>` (exec form)
                   agents/     wikory-recorder, wikory-consolidator
                   commands/   /wikory:*
                   MCP         registered at plugin level
  codex-plugin/    hooks -> same binary; agents/*.toml            (after stage 0)
  any harness      AGENTS.md contract: `wikory context | record-turn`  (CLI-only)

wikory binary (Go, static, one per platform)
  hook | context | search | read | record-turn | record-report | consolidate
  promote | refresh | doctor | trust | bootstrap | backup | restore | migrate
  compact | merge | purge | forget | export | statusline | mcp   (later: serve, integrate)

Storage
  <repo>/project-memory/         committed: wiki/, turns/ (promoted only), log.md, schema/, config.toml, .gitattributes
  <user-data>/wikory/<key>/      private: turns/, raw/, personal-wiki/, wikory.db (SQLite: FTS5, links, ingest state, queue), locks/
```

- **Language and runtime.** Go, a static single binary for darwin, linux and windows, pure-Go SQLite (`modernc.org/sqlite`) and the official Go MCP SDK. No resident daemon. A SQLite queue gives recoverability. Python is used only by the benchmark harness, which never ships.
- **Operations layer.** CLI, MCP server and (later) the web server are thin wrappers over one internal application layer. Contract tests run each operation through every wrapper.
- **`--json` contract.** Every command has a versioned `--json` output. It is the API for MCP, the benchmark harness and any future UI.
- **Rejected alternatives.** Markdown plus instructions only (no enforcement), an engine inside CodeGraph (couples two products), a Python engine (weak Windows story), a daemon (operational burden), Bun (weakest Windows support) and Rust (no gain over Go here).

## 4. Identity, storage and team mode

### 4.1 Project identity

`wikory init` generates a `project-id` and writes it to the committed `project-memory/config.toml`. Identity is not derived from a path or a remote URL, so clones, renamed folders and teammates agree on it. Every worktree resolves to the same project by finding that file in its checkout. Git is required in v1, because scope fields depend on branches and revisions.

### 4.2 Committed store

```
project-memory/
  config.toml   project-id, schema_version, retrieval budget, source scope and exclusions
  schema/       page types, claim schema, writing rules (what the subagents are briefed with)
  wiki/         one topic per file, markdown plus frontmatter, claims as stable-id blocks
  turns/        promoted turns only
  log.md        append-only, one line per publish event
  .gitattributes  `log.md merge=union` and `** text eol=lf`
```

`index.md` is **not committed**. It is a generated catalogue written locally by `wikory refresh` (and available through `wikory export index` for publishing), because regenerating it on every publish would guarantee merge conflicts.

### 4.3 Private store

Per machine, mode 0700 directory and 0600 files on macOS and Linux, owner-only ACL on Windows, in a local (non-roaming) user-data directory. Contents: `turns/` (captured turn records, private by default), `raw/` (transcript pointers and short redacted extracts only, never full transcripts), `personal-wiki/` (the wiki Wikory writes in Personal trust mode, see 10.4, so that nothing is ever written into a repository you do not control), `wikory.db` and `locks/`.

**Store key.** The store is keyed by `project-id` plus the canonical origin (falling back to a hash of the repository path when there is no remote). The canonical origin is the remote URL normalised to lower-case `host/owner/repo`, with any `.git` suffix, scheme and credentials removed and SSH and HTTPS forms treated as equal. The store records every origin it has been known by, so adding a remote to a local repository, switching between SSH and HTTPS, or moving the repository to another owner does not orphan it. `wikory store rekey` handles anything else. Forks and unrelated clones that carry the same `project-id` get separate stores. Multiple clones of the same origin share one store, protected by the lock, and worktrees share it already. `wikory purge --orphans` removes stores whose checkout no longer exists.

**Checkout registration.** The store key is not a trust boundary, because the origin recorded in a clone's `.git/config` can be copied from another project, for example by an archive that ships its own `.git` directory. Each checkout (identified by its git common directory) is therefore registered separately in the store. An unregistered checkout is attached to an existing store only after it passes first-contact trust (10.4), and until then it receives no private data, including pending summaries, through retrieval.

**Durability classes.** The wiki index in `wikory.db` is derived from the committed files and rebuilt by `wikory refresh`. Turn records and the queue are not rebuildable, which is why backup (4.7) exists.

### 4.4 Worktrees and branches

Committed pages live in each worktree's checkout and diverge by branch exactly as code does, and scope is enforced by what is present on disk. `wikory.db` holds the shared infrastructure for the whole machine (turn records, queue, locks, trust ledger), but its wiki index is **per checkout**: pages are indexed by checkout, relative path and content hash, and retrieval serves only claims whose file is present in the current worktree's tree. A claim published from a feature-branch worktree exists only in that checkout until the file is committed, merged and pulled, so other worktrees cannot see it. Committed metadata therefore carries only `scope=repo`. The originating branch, worktree and revision are kept in the private store for bookkeeping and are never used to decide retrieval. On merge, `refresh` re-validates the claims now present in the merged tree against current evidence. A discarded branch's claims disappear from every live checkout because their files do, and `refresh` expires their index entries. Revisions recorded on claims are hints. Rebase, squash merge, force push and shallow clones can make a recorded revision unreachable, so identity rests on content hashes (5.5).

**Checkout binding.** Isolation covers the whole pipeline, not only the index. Every turn record, pending-summary query, consolidation job and publish destination carries the identity of the registered checkout it came from (4.3). The briefing lists pending summaries from the current checkout only and gives a count for others. A consolidation job reads turns from its own checkout and publishes only into that checkout. Consolidating turns from another checkout is explicit (`wikory consolidate --checkout`) and never a side effect. If the originating checkout no longer exists, its turns wait for an explicit decision and are never applied to a different tree.

### 4.5 Team mode

Git is the merge. Rules that keep merges boring:

- One topic per page and one claim per block with a stable id, so edits are line-local.
- `log.md` is append-only with `merge=union` in `project-memory/.gitattributes`, which needs no per-clone configuration. The same file sets `** text eol=lf`, so Windows checkouts do not rewrite the files.
- `index.md` is not committed (4.2), so it cannot conflict.
- Two branches that append claims at the same place in a page still conflict in git: stable ids make edits line-local only when they do not touch the same lines. `wikory merge` reconciles conflicted pages by claim id and re-validates them, and `wikory doctor` points at unresolved conflict markers.
- One publishing writer per machine, enforced by a lock in the private store. Publish is write to temp file, validate, atomic rename, then update the database.

**Wikory never runs `git commit` or `git push`.** It writes files into the working tree and they appear as ordinary diffs, so each repository's own commit and review policy stays in force. `wikory doctor` reports uncommitted wiki changes so they are not forgotten. Recommended: `CODEOWNERS` on `project-memory/`.

### 4.6 Schema evolution

`config.toml` carries `schema_version`. Migrations are forward-only and additive, run by `wikory migrate`, which writes a backup first and does a dry run by default. A binary that finds a wiki newer than it understands goes read-only with a notice and never writes. The wiki format changes only with a major version. Fixtures from every released schema stay in the test suite permanently.

### 4.7 Backup

Unpromoted turn records are not rebuildable, so `wikory backup` and `wikory restore` produce and read a versioned archive of the private store.

- Backups are **encrypted by default** (age, pure Go). The key is held in the OS keychain with a printed recovery key or a passphrase option. Losing both makes a backup unreadable, and the first run says so. The recovery key is displayed only to a human on a real terminal, never in the output of a command an agent runs (10.4).
- The destination must be **off-device**: an external disk or a locally mounted or synced folder (iCloud Drive, Dropbox, OneDrive, Google Drive). `backup` refuses a destination on the same volume as the data directory unless explicitly overridden, and warns for synced folders it cannot recognise.
- Because the binary makes no network calls, there is no built-in cloud upload and no sync in v1.
- `wikory doctor` reports store age and the last off-device backup, and warns when none has been made after a configured time or number of turns. Promoting a turn through git is the other off-device route.

### 4.8 Growth control

Left alone, a wiki grows until retrieval gets noisier and merges conflict more. The consolidator, under validation, applies: a page-size cap with a split rule, merging of duplicates, and `wiki/_archive/` for claims that have been superseded for a configurable period. Archive is excluded from retrieval unless requested. `wikory compact` runs the same operation on demand with a dry run first. Deletion is never automatic. Thresholds are tuned on the pilot, not fixed here.

## 5. Data model

Four record kinds: source, turn, claim, page. The claim is the unit of memory.

### 5.1 Source

A reference, never a copy: `id`, `kind` (code, doc, handoff, memory-file, commit, test-result, user-statement, external), `locator` (repository-relative path, symbol, commit sha or external reference), `content_hash`, `revision` and date where available. Line numbers are a hint only, never identity.

### 5.2 Turn

Private unless promoted.

- **Identity:** `turn_id` (a deterministic hash of session id and turn-start marker, so replays are idempotent), `project_id`, `harness`, `session`, `parent_turn` (for subagent attribution), `source` (`turn` or `report`), `reporter` (for reports), and `worktree`, `branch`, `revision` plus fingerprints of uncommitted files.
- **Content:** request and constraints, outcome (completed, partial, interrupted, failed), decisions with stated rationale, files and symbols touched, failed approaches, open questions, and `preferences[]` (user corrections and stated preferences, private only).
- **Verification entries:** each carries `observed: true` only when the binary saw the corresponding tool result. An agent saying that a test passed is stored as `observed: false`.
- **Processing:** `state` (captured, sealed, summarised, validated, consolidated, failed) and `summary_origin` (deterministic, recorder, agent, fallback).

### 5.3 Claim

| Field | Values |
|---|---|
| `id` | stable ULID, never reused |
| `kind` | current-impl, intent, rationale, hypothesis |
| `status` | verified, unverified, conflicting, historical, superseded |
| `evidence[]` | source id plus the content hash seen at verification time (the git blob OID) |
| `verified_at`, `verified_revision` | when, and the revision at which it was verified. The revision is a hint only, never identity |
| `scope` | repo-wide in committed metadata. Branch, worktree and originating revision are kept in the private store only |
| `depends_on[]` | paths and symbols |
| `supersedes`, `contradicts` | claim ids |
| `origin_turns[]` | turn ids |
| `aka` | short list of alternative phrasings for retrieval, length-capped |
| `th` | hash of the claim text |
| `author` | agent or human |

`unknown` is deliberately not a claim status. It is an absence, recorded in the bootstrap coverage report.

**On-disk format.** One paragraph per claim, preceded by a single-line HTML comment carrying the metadata. It is invisible when rendered, line-local for merges and greppable:

```markdown
<!-- claim c=01J8Q... kind=current-impl status=verified vrev=a1b2c3d scope=repo ev=src/queue.ts@9f3e1a2 dep=src/queue.ts#enqueue th=7c1e2f aka="retry;replay" -->
Jobs are acknowledged only after the handler returns, so a crash replays the job.
```

**Canonical form and encoding.** The text hash `th` is taken over the canonical form of the claim text: LF line endings, Unicode NFC, trailing whitespace trimmed. A Windows `autocrlf` checkout or a macOS normalisation difference therefore does not read as a human edit. Values in the metadata comment use a restricted alphabet: paths and `aka` entries are percent-encoded where they contain spaces, `;`, `=`, quotes, `>` or a double hyphen, path separators are normalised to `/` on every platform, and the comment never contains `--`, which HTML comments forbid.

### 5.4 Page

Frontmatter: `id`, `type` (component, decision, lesson, failure-signature, runbook, concept, file-companion), `title`, default scope and typed forward `links`. **Backlinks are never stored.** They are derived in the database and rendered only into `index.md`.

### 5.5 Rules enforced by the validator

1. **No self-evidence.** A claim whose only evidence is an agent-generated summary cannot be `verified` and cannot be `current-impl`. It stays `hypothesis` or `unverified`.
2. **Resolvable evidence.** Evidence identity is the git blob OID of the evidence file. A claim can be `verified` only if its evidence blobs exist in the tree at `HEAD`, because evidence taken from a dirty working tree can never resolve from a teammate's clone. A claim whose evidence is uncommitted stays `unverified`, and the next refresh upgrades it once the evidence is committed. Where a recorded revision is no longer reachable (rebase, squash merge, force push, shallow clone), verification falls back to equality between the stored blob hash and the current tree. Acceptable evidence is a repository path at a committed blob, a commit, a committed document, or a promoted turn. A private turn id alone does not count. Sources untracked by git (handoffs, for example) have no blob, so claims derived from them cannot reach `verified` through that source alone. Only code evidence can verify them.
3. **Staleness is computed, not stored.** Committed files record only the last verification. At retrieval the binary compares current hashes of `depends_on` with the stored evidence hashes. Committed diffs therefore do not churn when a source changes.
4. **Binary-authored metadata.** The consolidator emits structured claim proposals (text, kind, evidence locators, `depends_on`). The binary mints ids, computes hashes and revisions, and renders the metadata line. A model never writes it.
5. **Human edits.** If a claim's canonical text no longer matches `th`, the claim is flagged edited, drops from `verified` to `unverified`, and records `author=human`. The next refresh or consolidation re-verifies it against evidence, or the human adds evidence. Human intent is kept, and is not presented as verified.
6. **Committed status is re-derived locally.** A committed `status=verified` is an assertion by whoever wrote the file. Retrieval shows a claim as verified only after its evidence resolves and the blob hashes match in this checkout, and otherwise as `asserted verified, not locally confirmed`.
7. **Confined, race-resistant file access.** Each source class has an authorised root: the repository root for repository files, the private store for Wikory's own data, and explicitly approved roots for outside sources (such as a Claude memory directory approved at bootstrap plan). Every open is made relative to its root with no-follow semantics applied to every path component, not an `lstat` followed by a separate open, which races and misses intermediate directory symlinks. Rooted-open facilities such as Go's `os.Root` are used where the minimum Go version supports them, with reparse-point checks on Windows. Symlinks and non-regular files are refused, and a path is refused if it leaves its root. Acceptance tests cover symlinked files and directories, replacement between check and open, and Windows junctions.

### 5.6 Evidence kinds

| Knowledge | Evidence | Treatment |
|---|---|---|
| Current implementation | Code, configuration, observed behaviour | Re-check when sources change |
| Intended behaviour | Requirements and explicit user decisions | Preserve even when implementation differs |
| Historical rationale | Attributable discussions, notes, decision records | Retain source and historical context |
| Hypothesis | An inference or proposed explanation | Label unverified until supported |

Recent text does not automatically outrank older authoritative evidence. A requirement describes intent, not implementation.

## 6. Turn lifecycle

```
Stop hook (binary, deterministic, no model call)
  1 exclude    memory job or continuation loop? exit
  2 capture    deterministic turn record (state=captured)
  3 triage     flagged as decision-bearing? else done
Stop continuation (flagged turns only) -> working agent dispatches wikory-recorder
Recorder subagent (small tier, MCP tools only)
  4 summarise  reads its slice through a job-scoped MCP read, submits via record_turn
Binary
  5 validate   schema, evidence, observed flags, secret scan -> state=validated
  6 trigger    threshold / decision-bearing / explicit -> Stop continuation
Consolidator subagent (mid tier, MCP tools only)
  7 propose    structured claim proposals into the private job dir, not the wiki
Binary
  8 publish    re-check, atomic write, log.md line, db commit
  9 index      backlinks and the local (uncommitted) index.md regenerated
```

**1. Self-exclusion and job lifecycle.** The binary cannot spawn agents. It offers a job: when a continuation asks the working agent to dispatch the recorder or consolidator, the brief carries a job id and nonce, and the binary records the job as `offered`. The subagent passes the id back on its first MCP call, which makes the job `active`. The job is `closed` when its submission is accepted and `expired` when its lease runs out. A Stop or SubagentStop event is dropped as memory work only if it carries the `agent_id` or `agent_type` of a memory subagent, or matches an active job's nonce. Matching on session alone would also drop the working agent's own Stop, because subagents may share the parent's session id (to be verified, 14.1), and that Stop is the one that can trigger consolidation. Sub-agents of the working agent attach to the parent turn via `parent_turn`. Continuations are capped at two per turn and `stop_hook_active` prevents loops. The harness imposes its own cap on consecutive continuations.

**2. Deterministic capture.** The hook computes `turn_id` and builds a turn record from structured payload fields and the transcript: the final assistant message (taken from the payload, because the transcript can lag at stop time), files touched, commands run, observed tool results, git facts, and a transcript pointer (path, start offset, end marker). The write goes to a temp file, is fsynced, then renamed. Capture is conditional and monotonic: the record is created only if `turn_id` is absent, a duplicate Stop never overwrites an existing record, and state moves only forward (`captured`, then `sealed`) through compare-and-swap updates in the database, with each enrichment written as a new immutable version of the turn file rather than an in-place replacement. The `UserPromptSubmit` hook also registers a durable turn-start marker, so a turn interrupted before its first Stop is still detected. The record starts in state `captured`. Because the transcript is written asynchronously, tool results and the final turn boundary may not be present yet, so the next hook event, `SessionStart` or consolidation re-reads the slice from the stored pointer and completes the record, moving it to `sealed`. Completion only fills in missing fields and never changes a sealed record. A record is sealed when the transcript slice from the stored pointer contains the end marker and the final message, or after a configured timeout, when it is sealed as `incomplete`. This record is the floor. It exists whether or not any model ever enriches it.

**3. Triage.** A deterministic heuristic flags a turn as decision-bearing: a user correction, a failed-then-changed approach, explicit decision language, a spike in files changed, or an explicit `wikory record`. Only flagged turns cause a Stop continuation that asks the working agent to dispatch the recorder. Unflagged turns are left deterministic and batch into the next consolidation. Forcing a named subagent from a Stop hook is not a documented pattern, so recorder dispatch is best-effort. If the agent does not dispatch it, nothing is lost: the deterministic record stands. The consolidator can also enrich unflagged turns in batch from their pointers.

**Dispatch without making the user wait.** A Stop continuation keeps the working agent running until the dispatched subagent finishes, so the user would wait at every flagged turn and the working model would spend context on dispatch. Plugin agents support a `background` setting, so the preferred design dispatches the recorder and consolidator as background subagents that return at once. Whether background subagents fire `SubagentStop`, whether the continuation ends cleanly, and how continuation messages appear in the transcript are stage 0 checks. If background dispatch is not viable, consolidation is deferred to the start of the next turn, and recording stays limited to flagged turns.

**4 to 5. Recorder and validation.** The recorder reads the transcript slice itself and submits its output through the `record_turn` MCP tool. Validation is the binary's job: schema, that referenced paths exist at the recorded revision, evidence hashes, the section 5.5 rules, the secret scan, and matching verification commands against real tool results to set `observed`. A failure sets `state=failed` with a reason and keeps the turn. Nothing is silently dropped.

**6 to 8. Consolidation and publish.** Consolidation is requested only by the events in the trigger table below. The binary briefs the consolidator with pending turns, affected pages and their current hashes. The consolidator submits structured proposals to a job directory. Publish then follows the protocol below. Only a headless worker (section 8.6) can consolidate at `SessionEnd`, because the agent is gone by then.

**Trigger table.**

| Event | State | Action |
|---|---|---|
| Stop, turn flagged decision-bearing | any | Continuation asks the working agent to dispatch the recorder (in the background where viable) |
| Stop, validated pending turns in this checkout reach the threshold, flagged or not | no consolidation job `offered` or `active` for this checkout | Continuation asks the working agent to dispatch the consolidator. Unflagged turns count toward the threshold, so a quiet workload cannot accumulate indefinitely |
| Stop, any | a job is `offered` or `active` | No new continuation |
| `wikory consolidate` (human or slash command) | any | Offers a job immediately |
| Background dispatch not viable (stage 0) | consolidation due | A one-line `UserPromptSubmit` nudge, emitted only when consolidation is due, asks the agent to dispatch at the start of the next turn. This is the only case where `UserPromptSubmit` injects anything while the per-prompt push is off |
| Subagent fails or its lease expires | job `expired` | Turns return to pending and the next qualifying Stop offers a new job. After three consecutive failures the briefing says so (8.8) |
| Recorder finishes after the parent's Stop | turn `captured` or `sealed` | Submission is accepted if the job is still `active`. Otherwise the deterministic record stands and the turn waits for the consolidator's batch enrichment |
| `SessionEnd` | any | Only a headless worker may consolidate |

**Publish protocol.** A publish changes several pages, `log.md` and the database, and rename is atomic only per file, so publishing is manifest-based.

1. Under the publish lock, check the job's lease epoch. Every lease carries a monotonically increasing epoch and a worker holding an expired epoch is refused, so an old worker cannot publish after another has taken over. Then revalidate: the checkout is the job's checkout, its tree fingerprint (the `HEAD` tree OID plus the blob OIDs of the evidence the proposals use) still matches the one captured when the job was offered, and each target page's current hash equals the proposal's base hash. Any mismatch aborts the publish and queues a retry, because the binary cannot spawn agents itself. Files changed by an external editor or a branch switch while the job ran are conflicts and are never overwritten.
2. Stage every new page version in the job directory and write a manifest listing each operation with its old hash, target hash and state.
3. Apply the operations one by one by atomic rename, marking each in the manifest and the database.
4. Append one `log.md` line keyed by the job id, so replays are idempotent.
5. Commit the database transaction and mark the turns `consolidated`.

Recovery reads the manifest. An operation whose file has the old hash is retried, one at the target hash is marked done, and a file with any other hash is a conflict that stops the publish and is reported, so a human edit made after a partial publish is never overwritten blindly.

**Checkpoints.** `PreCompact` writes a deterministic checkpoint (a compaction marker inside the current turn, not a turn boundary) and never blocks compaction. Because the recorder reads the transcript slice itself from `transcript_path` and a turn-start marker, and the parent passes only pointers and a short structured summary, autocompact cannot thin the record. `SessionStart` (startup, resume, compact) injects the briefing with pending-turn summaries inline, so an interrupted or unconsolidated period is disclosed, not hidden.

**Crash recovery.** Jobs hold a lease. Any `wikory` invocation or `SessionStart` reclaims expired leases and returns their turns to pending. Publish recovery follows the manifest protocol above, which handles a crash at any step, including between the first and last rename. Interrupted turns that produced no Stop event are detected by scanning the stored transcript pointers (a new session's payload carries no prior transcript) and receive an `interrupted` stub. Lease reclaim, that scan and the trust ledger check run in a short-lived detached process or lazily at the next operation, so the `SessionStart` hook itself stays within its time budget.

**Failure visibility.** Pending, failed and leased counts feed the briefing, the status line and `wikory doctor`. A failed update never blocks coding work and never reports that memory is current.

## 7. Retrieval

**Two paths.** Push: `SessionStart` (startup, resume, clear, compact, fork) injects an orientation briefing. Pull: afterwards the agent calls `context`, `search` and `read` through MCP (or the CLI where MCP is absent). A `UserPromptSubmit` push per prompt exists as a config switch, default off, because it costs tokens on every prompt and the benchmark should decide whether it earns them.

**Briefing layout**, default budget 1,500 tokens, config-tunable with a hard character cap well below the harness limit for injected context (documented as 10,000 characters on Claude Code):

1. A status line first: last consolidated turn, pending N, failed M, stale K, and a plain sentence that the wiki may not be current.
2. Pending-turn summaries inline from the current checkout only (other checkouts appear as a count), newest first, capped at about a quarter of the budget. `context` never waits for consolidation.
3. Orientation items: active decisions, known failure signatures, open questions, components touched by recent turns on this branch. Titles and one-liners only.
4. Pointers for deeper search and read.

Truncation is deterministic by priority and never cuts mid-claim. It ends with "N more available". Token counts are estimated by the binary with no tokeniser dependency.

### 7.1 Search

SQLite FTS5 with BM25 over page titles, headings and claims, weighted title over claim over body. The binary expands identifiers and paths into several forms (`enqueueJob`, `enqueue_job`, `enqueue job`, path segments) in a search-text column before indexing, and that column is indexed with a built-in FTS5 tokeniser, so no custom tokeniser has to be registered with the SQLite driver. Claim `aka` phrasings are indexed alongside the claim text.

**Scope filter runs before ranking.** The index is per checkout (4.4), so a claim is eligible only if its file is present in the current worktree's tree. Recorded revisions and branch names are never used to decide eligibility. Superseded claims are excluded by default. Historical rationale is shown, labelled, for why-questions or with `--history`. Archived claims are excluded unless requested.

### 7.2 Freshness at read time

For each returned claim only, the binary hashes the `depends_on` sources and compares them with stored evidence hashes. Output reads `verified @a1b2c3d, sources unchanged`, or `STALE: src/queue.ts changed since verification`. Renames found by diffing `verified_revision` against `HEAD` are flagged `moved to <path>`. Deleted sources are flagged `source removed`. A committed `status=verified` is shown as verified only when the evidence also resolves and matches locally (5.5 rule 6), and otherwise as `asserted verified, not locally confirmed`.

### 7.3 Retrieval ladder

| Tier | Content | Gate |
|---|---|---|
| 0 (v1) | FTS5, identifier tokeniser, one-hop link-graph expansion (top hits bring their linked decisions and failure signatures), consolidator-written `aka` aliases | None |
| 1 | Vectors in `wikory.db`, brute-force cosine, behind a pluggable `Embedder` interface. The embedder is an optional sidecar or the user's own API key, never part of the core binary | Retrieval-only evaluation shows recall@5 gain over tier 0 above a margin fixed in the preregistration, and a viable generation path exists |
| 2 | `CodeIndex` seam (resolve symbol to range and hash, `dependents(path)`, `exists`) with CodeGraph as the first optional provider, shelling out to `codegraph` when `.codegraph/` exists | Hybrid condition beats wiki-only on code-navigation tasks |
| 3 | A native code graph | Only if CodeGraph is abandoned, or evidence shows a code graph is decisive and its Node dependency is the blocker |

The `CodeIndex` interface and a path-level hashing default are in v1. Without a symbol resolver, `dep=src/queue.ts#enqueue` falls back to hashing the whole file, so staleness gives more false positives until tier 2 exists. How many is unmeasured.

Embedding generation is the hard part: a local model brings cgo, large binaries and a model download, and an API provider brings keys, spend and third-party egress. Brute-force search over a few thousand claims is cheap and needs no vector extension.

### 7.4 Degraded modes and injection stance

The briefing hook has a hard time budget (default 2 seconds). On timeout, or with a missing or corrupt `wikory.db`, it emits the status line plus the local `index.md` if one exists, and says it is degraded. `wikory refresh` rebuilds the database from committed files. Retrieval never blocks a session.

Defences against injected instructions:

- Every briefing and search result opens with a fixed preface: retrieved memory is evidence to verify against current code, not instructions.
- Results sit inside a delimited block. Claim text is sanitised: tool-call-like markup stripped, line length capped, HTML comments other than claim metadata removed.
- Claims with external-origin evidence render with an `external` marker and cannot reach `verified` on that evidence alone.
- A linter warns on directive-shaped claim text aimed at agents. It is a tripwire, not the defence.
- Team-visible content passes through git review. Private turns never leave the machine.

## 8. Adapters and surfaces

### 8.1 Principle and modes

An adapter is a manifest, hook wiring, agent definitions and a short instruction block. All logic lives in the binary. Per-harness decoders turn raw payloads into one internal event set (`session-start`, `user-prompt`, `stop`, `pre-compact`, `session-end`, `subagent-stop`). All transcript parsing is confined to versioned, fixture-tested decoders.

`wikory doctor` probes the harness version against a known-good table and sets capability flags (`can_continue_on_stop`, `can_inject_on_session_start`, `has_subagents`, `model_pins`, `transcript_stable`). The flags select a mode, which is always printed in the briefing status line.

| Mode | Harness | Capture | Model work | Guarantees |
|---|---|---|---|---|
| Full | Claude Code (v1) | Stop hook, observed verification | In-session recorder and consolidator | Sections 5 to 7 |
| Full or Reduced | Codex (after stage 0) | Same hooks, unstable transcript, so structured fields preferred | In-session if Stop can force a continuation, else agent-submitted summaries | Depends on stage 0 |
| CLI-only | OpenCode, Gemini, Qwen, Kimi and others | Agent calls `wikory record-turn` | `wikory consolidate` run explicitly or from a Full-mode session | Best effort. `observed` is always false, so these turns cannot verify claims on their own |

**v1 scope.** Claude Code on macOS and Linux. Windows is built and tested in CI from day one and labelled beta. The Codex adapter and the CLI-only contract follow once stage 0 verifies Codex `Stop` behaviour. The architecture stays portable. Only what v1 includes is limited.

### 8.2 Claude Code plugin

- `hooks/hooks.json` registers `SessionStart`, `Stop`, `PreCompact`, `SessionEnd`, `SubagentStop`, `UserPromptSubmit` (records a turn-start marker and otherwise returns instantly while the per-prompt push is off) and `PreToolUse` on Bash (denies agent calls to the human-only commands, see 10.4), each as a direct `wikory hook <event>` call in exec form with a real executable, which works on Windows without a shell script. Hook timeouts are set low in the plugin. `PreCompact` can block compaction on Claude Code, so Wikory's hook must never block it. `SessionEnd` has a short budget, so it only records and detaches.
- `agents/wikory-recorder.md` pins a small model tier and `agents/wikory-consolidator.md` a mid tier, using model aliases so they track releases (confirmed at build time). Both are declared `background` where stage 0 confirms that works (section 6).
- **Least privilege.** Memory subagents get the plugin's MCP tools only: no `Bash`, no `Read` and no network tools. A generic `Read` would let a poisoned transcript induce a read of any local file whose content is then submitted as a claim. The MCP server instead exposes scoped reads bound to the job (the assigned transcript slice, and repository files inside the confinement of 5.5 rule 7 with the secret deny list applied), and submission tools accept only content for the job's permitted sources. Plugin agents ignore `hooks`, `permissionMode` and `mcpServers` frontmatter, so MCP is registered at plugin level. Whether `tools` allowlists are enforced for plugin agents is a stage 0 check, and if it fails the design falls back to the narrowest tool set available and `doctor` says so. Subagents write only into the job directory through MCP and the binary publishes. Pattern scanning cannot reliably detect ordinary personal data, so what private turn content may become wiki text is governed by promotion and review (10.2, 10.3), not by the scanner alone.
- `commands/`: `/wikory:init`, `status`, `search`, `promote`, `consolidate`, `trust`.

### 8.3 MCP

`wikory mcp` is a stdio server with no network listener. It is the agent-facing contract in Claude Code and is registered by default. Tools: `context`, `search`, `read`, `status`, `record_turn`, `record_report`, the job-scoped read tools (`read_transcript_slice`, `read_repo_file`) and the submission tools used by the recorder and consolidator. `promote`, `trust` and `backup` are not exposed as MCP tools and are intended to be run by a human. Slash commands run through the agent, so for these the protection is the harness permission prompt plus the deny hook in 10.4, and it is advisory. Consolidation is triggered by the agent by design, through the continuation, and is not a human-only operation. Desktop chat clients (Claude Desktop, ChatGPT Desktop) get read-only access by default and the committed wiki only, unless `--include-private` is set. Whether ChatGPT Desktop supports local stdio MCP is unverified.

### 8.4 CLI

The CLI remains for the places an agent is not the caller: hooks (the same binary), humans (`init`, `doctor`, `promote`, `bootstrap` with a progress bar, `backup`), `wikory trust` (which needs a real TTY), the benchmark harness and CI (`--json`), and harnesses without MCP.

### 8.5 Orchestrated projects

Many users run a single agent. Some, including the pilot, run an orchestrator that dispatches workers.

- **The orchestrator owns project memory.** A user-level config maps an environment variable to a Wikory role (for example `ORCA_ROLE` to `worker` or `coordinator`). Wikory hardcodes nothing about any orchestrator. A `worker` role does no capture.
- **Workers do not load Wikory in v1.** The orchestrator runs `context` for the task and puts the result in the dispatch brief.
- **`record_report`.** The orchestrator submits a structured worker report (task, outcome, files, commits, decisions). The binary verifies the git facts itself (commits exist, changed files match). Anything git cannot confirm, such as "tests passed", is stored as asserted. The orchestrator runs consolidation and its own turns are captured normally.
- **Integration packs.** A pack is a manifest (detection signals, snippets, README) under `integrations/<name>/`. v1 ships `integrations/orca/` as a static, documented pack: a snippet for the orchestrator's rules to call `record_report` after each worker completion, plus an optional hook the user installs themselves that only injects a reminder and never auto-records. Packs ship with Wikory and are never loaded from a project repository. A later `wikory integrate` command detects multi-agent setups, asks permission to probe configuration (listing exact paths, structure only, never values), and proposes a pack as a file to review. It is built when a second pack exists.
- Read-only worker access to memory is a later, evidence-gated option.

### 8.6 Headless worker (opt-in)

`[worker] mode = "headless"` runs `claude -p` or `codex exec` as a detached child, starting with an authentication and credit probe. On any failure it falls back to in-session work and writes a `fallback` event that counts in the status line and `doctor`, so a fallback is never silent. It is the only mode that can consolidate at `SessionEnd`. Default concurrency is 1. Wikory never stores harness credentials. This mode is user-level only and cannot be enabled from a repository's `config.toml`.

### 8.7 Install and compatibility

Two steps for v1: install the signed `wikory` binary onto `PATH` (releases, Homebrew, Scoop, `go install`), then install the plugin. `wikory doctor` is the first command after install. A missing binary produces one clear install line. The plugin manifest declares a minimum binary version and the binary declares its supported harness range. A mismatch degrades the mode and says so. One-step bundling is revisited after the stage 0 packaging check.

### 8.8 Status line

An additive Wikory segment (for example `wikory: 3 pending, 1 failed`), on by default and switchable off. Claude Code runs one `statusLine` command per user, so being additive means wrapping the existing one: `wikory statusline` runs the user's command with the same input and appends its segment. It reads a small status file the binary maintains, not the database. On any error it falls back to running the original command alone. Wrapping edits user settings, so it asks once, shows exactly what changes, and is reversible with an unwrap. Whether a plugin may supply or wrap a status line is a stage 0 check. A threshold notice in the briefing (for example three consecutive consolidation failures, or pending work older than a day) applies regardless.

### 8.9 Claude Code auto memory

Claude Code exposes only `autoMemoryEnabled` and `autoMemoryDirectory`, with no read-only or write-only mode. Wikory therefore does not depend on it. `memory/` is a bootstrap source. A user-level `[claude_memory] posture` is `coexist` (memory stays on, new memory files are ingested as attributed, unverified sources on refresh) or `retired` (memory off, reversible). Wikory captures preference-type notes privately in the turn record's `preferences[]`, and `wikory export claude-memory --since <date>` regenerates memory-format files for any gap, so switching memory back on loses no period. Exported notes are Wikory-authored and will differ in wording from notes the agent would have written. A stage 0 spike tests whether redirecting `autoMemoryDirectory` can give a true write-only route, with export as the fallback.

### 8.10 Later: web app and MCP App

`wikory serve` (a local web app) and an interactive MCP App are later versions. The web app's bar is Nimbalyst-level feature coverage with a far better UX, built behind a UX spec and a design step using the `frontend-design` skill before any code. Trigger: review or promotion friction observed on the pilot, or adoption by teammates who will not use a terminal. Prerequisites in v1: the versioned `--json` contract, a single application layer and versioned schemas. The framework (React or Svelte, not both; Svelte 5 with TypeScript and Vite as a static single-page app is the current lean) is decided in that spec. `go:embed` needs built assets at compile time, so `go install` from source would not include the app unless built assets are committed. Choosing between committing them or restricting the app to release binaries is an open item. Local only: 127.0.0.1, a per-run token exchanged into an HttpOnly cookie and stripped from the URL, Host and Origin checks, no CORS, strict CSP.

## 9. Bootstrap for existing projects

Build an explicitly labelled current-state baseline. Never invent historical turns or infer a rationale that nobody recorded.

```
wikory bootstrap plan              inventory, scope, cost estimate. No model calls.
wikory bootstrap run --sample N    proof run on N sources, measured quality and cost
wikory bootstrap run               full run, only after explicit confirmation
```

The plan and the sampled proof run form a cost gate: token maths first, a currency estimate only where a pricing table is configured (on a subscription plan there is no per-token price, and the gate says so rather than inventing one), a measured sample, then explicit confirmation. The run is staged, checkpointed in `wikory.db`, resumable and idempotent by content hash.

**Where model work runs.** The binary does no model work itself, and stages 4 and 5 need many small and mid-tier subagent calls. They run either as in-session batches driven by `/wikory:bootstrap`, where the working agent dispatches subagents per batch and the binary tracks progress, or through the opt-in headless worker (8.6). Bootstrap cannot run purely as a terminal command without one of these. The in-session route spends the working agent's context on dispatch, which is one reason the plan reports the batch count before the run.

**Stages.**

1. **Inventory (deterministic).** Checkout tree, `CLAUDE.md` and `AGENTS.md`, docs, handoffs, planning folders, decision records, git history, branches and worktrees. Outside-repository sources (Claude `memory/`, an existing wiki) are read through pluggable `SourceReader`s and are opt-in per source, listed in the plan. Exclusions and secret scanning apply first. Secret-bearing spans are skipped and counted, never quoted.
2. **Register sources.** Kind, locator, content hash, date, and a revision where git has one. Trust tiers: code, configuration and tests establish current implementation. Decision records establish intent. Handoffs, memory notes and agent summaries are attributed claims.
3. **Prioritise (deterministic, cheap).** Rank components by recent churn, references from docs, open items in handoffs and entry points. Cover the top N within budget. The rest are reported as not inspected.
4. **Extract (small-tier subagents, per source).** Each claim carries a verbatim excerpt reference so the binary can confirm the quote exists in the source. Extracted claims start `unverified` or `historical`, never `verified`. Sources are untrusted data, so instructions inside a handoff never become instructions.
5. **Verify against code (mid-tier).** For prioritised components a verifier inspects code, configuration and tests and returns confirmed, contradicted or not-determinable, with evidence locators the binary resolves. Confirmed becomes `verified` on code evidence. Contradicted becomes `conflicting`, keeping both attributions with no silent winner. Not-determinable stays `unverified`.
6. **Baseline pages.** Purpose, architecture, component companions, constraints, known issues, unfinished work. Every page and claim is labelled with the bootstrap run and revision, with origin `bootstrap:<run-id>`. `turns/` is untouched. Where rationale is unavailable, the page says so.
7. **Coverage report.** Sources read, skipped and why, components verified, conflicting or unknown, claim counts by kind and status, tokens, cost and duration, available as `--json`. Real turn capture starts from the run's end marker.

**Session transcripts are excluded from bootstrap by default.** They are large, noisy and privacy-sensitive, and mining them is the closest thing to inventing history. `--include-transcripts` is available for sampled runs only.

**Re-runs and retirement.** Re-running reprocesses only changed sources. Retiring an old memory system for a project is a checklist the report prints. Wikory never edits or deletes those sources and keeps their source records for provenance.

**Progress and long-running operations.** One rule for the whole binary: any operation expected to take more than about two seconds prints an up-front notice and shows progress. The notice reads along the lines of "First run: reading 214 sources. This can take several minutes. Safe to leave running, it resumes if interrupted." In a terminal, a live bar shows stage, `n/N`, elapsed time and estimated time remaining, with one line per update when there is no TTY. In an agent session the command prints the notice first and runs in batches that each return a one-line status, which the agent mirrors on its task list. `--json` emits progress events, `wikory bootstrap status` reports the current stage, and the web app gets a progress panel. Estimates come from measured throughput per stage and are labelled as estimates before the first measurement. Hooks never block on this: if the index is still building, the briefing says so.

**Pilot (claudeclaw-ultra).** Baseline at default-branch `HEAD`, repo-wide scope. The worktrees are handled by `refresh` after merges, not bootstrap. In scope by default: tracked docs, `CLAUDE.md` and `AGENTS.md`, `.planning/`, handoffs, the Claude memory files and the existing wiki, all opt-in at plan time.

## 10. Security and privacy

### 10.1 Data classes

| Class | Where | Rule |
|---|---|---|
| Committed wiki | `project-memory/` | Shared through git, possibly public. Never holds secrets, personal data or private-turn content. |
| Private store | user-data directory | Owner-only permissions. Local, non-roaming. Backups and sync tools may still copy it, and the documentation says so. |
| Transcripts | harness | Never copied wholesale. Pointers plus length-capped, redacted extracts only. |

### 10.2 Secrets

Layered, and none of it is a guarantee.

1. **Redact at capture.** A pattern and entropy scanner replaces matches with `[REDACTED:kind]` before anything reaches the private store or a candidate page. Failure messages log only the kind and a hash prefix, never the match.
2. **Reject at validation.** Output containing a secret shape is rejected and the job marked `failed`.
3. **Scan at publish and at promote.** `promote` re-scans, shows the diff and needs explicit confirmation.
4. **Deny by default.** Wikory's own readers skip `.env*`, key files, credentials files and cloud configuration directories. This governs Wikory's readers only. An agent can still read what its harness permits.
5. **The real boundary is server-side.** `wikory doctor` recommends GitHub secret scanning and push protection. A secret that reaches a commit still needs rotating, not just deleting.

### 10.3 Least privilege and trust boundaries

- Memory subagents have MCP tools only: scoped reads bound to the job, no shell, no network and no generic `Read` (8.2). The worst a poisoned transcript can then achieve is a false or over-disclosing claim limited to the sources the job may read, which the evidence rules, git review or promotion review catch. The residual risk is a plausible claim that misreads real evidence, and ordinary personal data that a scanner cannot recognise.
- A repository's committed `config.toml` can only request risky capabilities (headless mode, sources outside the repository). Only user-level configuration enables them.
- Integration packs ship with Wikory and are never loaded from a project.
- Readers never follow symlinks (5.5 rule 7). In an attacker-controlled repository a symlink inside `project-memory/` or `docs/` could otherwise point at a file such as `~/.ssh/id_ed25519`, and its content would be indexed, served to the model and recorded in transcripts. The trust scan reports symlinks, and the readers refuse them regardless of the scan.

### 10.4 Project trust

`wikory trust` (and `/wikory:trust`) assesses a project and records a trust decision in the private store. **The assessment runs automatically on first contact, but the grant cannot be automatic.** A repository can contain exactly the files a team-adopted project contains, so adoption cannot count as trust: it is a claim the repository makes about itself. A project you create with `wikory init` is recorded as trusted automatically, because you made it.

**First contact triggers.** No trust record exists, and the project is either a newly cloned one, or one owned by someone else where you collaborate and Wikory is not adopted project-wide.

**The assessment** reports provenance (origin, owner, your relationship via `git remote` and `gh` where available), a scan of any committed wiki (claim count, directive-shaped text, external-origin claims, secret shapes, unexpected files such as executables and symlinks leaving the tree), and any capability requests in `config.toml`.

**Outcomes.** Team (you assert this project's team runs Wikory), Third-party (strict), Personal (private memory for you only, committed wiki ignored, and Wikory's own wiki written to `personal-wiki/` in the private store, so nothing is ever written into someone else's repository and no tracked file shows a diff), or Not now (inert, one-line status).

**Human-only grant, honestly stated.** The grant needs a real TTY (you run `wikory trust` in your own terminal) or a click in the future web app. A non-TTY call can assess and report, but an attempt to grant fails closed. A TTY check does not stop an agent that has a shell, because a pty wrapper satisfies it, so protection against a hostile local agent is advisory, in line with principle 6. The layers, none of them a hard boundary: the TTY requirement; a Wikory `PreToolUse` hook that denies agent Bash calls invoking `wikory trust` or `wikory backup` (best effort, since shell indirection can evade a pattern match); the harness permission prompt (these commands should never be allowlisted); and documentation. The threat this addresses is a repository's content persuading an agent to grant trust, not a determined attacker who already controls the agent's shell. The backup recovery key is shown only under the same conditions. How each harness runs a user-typed shell shortcut is unverified and not relied on.

**Trust is a ledger, re-checked on change.** The record stores a per-file hash ledger for `project-memory/`: hashes you approved plus hashes Wikory itself published. Your own publishes and uncommitted local edits never prompt. Anything that arrives by pull, merge or checkout whose hash is not in the ledger is drift. The check runs at serve time (every `context`, `search`, `read` and MCP call, a cheap hash compare, because an agent can pull mid-session), at `SessionStart`, and on demand. An optional git `post-merge` hook can run it right after a pull, installed only by explicit `wikory trust --install-git-hook`. On drift the diff-scoped assessment runs before anything from the changed files is served, and the transition table below decides the outcome. When a decision is needed, the committed wiki is withheld, a notice tells the agent to relay the outcome and ask you to run `wikory trust` in your terminal, and Wikory falls back to Personal mode until you decide. The assessment is diff-scoped: files changed, who changed them and through which merge or pull request, scan results on the diff, and any change to capability requests. Every transition is logged.

**Policy by class.**

| Class | Steady state | Asks you when |
|---|---|---|
| Team | Silent. Every pull still runs the diff-scoped scan | A scan hit, a new author touching `project-memory/` who was not in its history at your last grant, a `config.toml` change to capability requests, or a change of origin or owner. Content from other people's unmerged branches is labelled "unmerged, from `<author>`" and served only if the scan is clean |
| Third-party | Strict. Every drift is assessed and you are told the outcome | By default when the trust surface changed (`ask = "on-change"`). `ask = "always"` is a user-level setting that asks on every pull |

**Transition table.** One rule set applies before configuration, pages or cached results are served.

| Change | Team | Third-party | Personal |
|---|---|---|---|
| Wikory's own publish (recorded in the ledger by the publisher) | Accept | Accept | Not applicable: Personal writes to `personal-wiki/` |
| Uncommitted edit made outside Wikory (editor, script, agent) | Scan, accept if clean, ask if flagged | Ask | Not applicable |
| Arrives by pull, merge or checkout | Scan, accept if clean and no ask trigger, otherwise ask | Withhold until decided (`on-change` asks when files under `project-memory/` changed, `always` asks on every pull) | Not applicable |
| `config.toml` capability change | Ask | Ask | Not applicable |
| Origin or owner change | Ask | Ask | Re-run first-contact trust |
| Checkout not registered in the store | First-contact trust, no private data until granted | Same | Same |

An uncommitted edit is not assumed to be yours: nothing in the working tree proves who made it.

The new-author signal uses git author metadata, which anyone with push access can forge, so it is advisory. The ledger detects change, not authorship. Committed `status` and `th` values are unauthenticated text, which is why retrieval re-derives them locally (5.5 rule 6).

For a healthy team this is one prompt per clone, then silence. A user-level allowlist of owners or organisations (proposed) could pre-select Team for repositories that also pass a clean scan. It cannot be set from inside a repository. A clean scan cannot catch a plausible but false claim. For the Team class the real defence is review, which is why `CODEOWNERS` on `project-memory/` is recommended.

### 10.5 Surfaces and supply chain

- MCP: stdio only. `wikory serve`: see 8.10. Headless worker: 8.6.
- Minimal dependencies (pure-Go SQLite, MCP SDK) with pinned `go.sum`. Signed releases (Sigstore keyless signing), checksums and an SBOM. `wikory doctor` prints the resolved binary path and version, which matters because hooks resolve `wikory` from `PATH`.

### 10.6 Privacy and retention

Wikory makes no network calls and sends no telemetry. Memory subagent work goes to the user's existing model provider through their harness, the same provider that already sees the session. Tier 1 embeddings would be a new egress, so they are opt-in with an explicit disclosure. `wikory purge` removes turns and `wikory forget <claim-id>` removes a claim. Neither can remove content that has already been committed to git history, so a committed secret still needs rotating. Proposed default: unpromoted turns are kept 30 days after consolidation and extracts are purged sooner, both configurable. These figures are untested. `log.md` and database events record publishes, fallbacks and redaction counts, never redacted content.

## 11. Evaluation

**Home.** `bench/` in the repository, Python managed with `uv`, never shipped in the binary or plugins. The pilot is claudeclaw-ultra.

### 11.1 Conditions

| Condition | Assistance |
|---|---|
| B0 | Minimal baseline: repository instructions, `rg` and LSP only |
| B1 | Status quo: B0 plus the existing memory files and handoffs as they stood at the snapshot date (files dated later are removed, like every other later note). This is the condition Wikory has to beat |
| C | B0 plus CodeGraph (`codegraph init` first) |
| W | B0 plus the wiki, from a bootstrapped store |
| H | B0 plus both |

Two kinds of comparison are kept apart. Equal-source comparisons (for example B0 against C) isolate the effect of a retrieval or index tool on the same sources. Information-advantage comparisons (B1 and W against B0 and C) measure what memory sources add, and B0 and C are expected to miss handoffs and memory files. Ordinary source access (`rg`, LSP, direct reads) is equivalent in every condition. The benefit of accumulated turn history is reported separately.

### 11.2 Tasks

Real historical tasks replayed at a starting revision. Solution commits and notes dated later are hidden: history is cut at the snapshot, and bootstrap uses only sources dated up to the snapshot. Bootstrap is done once per snapshot, so tasks cluster at a few snapshot revisions. Families: implementation discovery, dependency tracing, rationale recovery, bug fixing, constrained feature change, fresh-session continuation, and stale-note or rename recovery (with deliberately injected staleness). Longitudinal sequences let memory accumulate along a fixed order, which is where the wiki should show distinctive value. Constraints that tasks test are planted only in sources dated before the snapshot. A condition that cannot see a given source (B0 and C cannot see handoffs and memory files) is expected to miss it, and that difference is part of what is measured, not a defect.

**Historical sources must be recoverable.** Filtering files by date does not remove later edits inside an older file. Historical replay therefore uses only sources whose content at the snapshot is recoverable from git history (tracked documents, handoffs and planning folders). Untracked, in-place-edited memory files such as a Claude memory directory cannot be reconstructed as of a past date, so they are evaluated prospectively: they are snapshotted at the start of a forward-looking study and tasks run after the snapshot. Each run uses its own immutable source snapshot and its own store namespace, so independent checkouts never share a store (4.3). In longitudinal sequences B1's agent auto memory accumulates along the same sequence as the wiki, inside the run's own namespace, so the comparison is between two accumulating systems.

### 11.3 Isolation

Per run: a fresh checkout, a fresh conversation and its own memory store. The same harness, model, settings and budget cap throughout. Order is counterbalanced and runs are repeated. Comparisons are within a harness (Claude Code first, then Codex), never across harnesses.

### 11.4 Measures

- **Primary:** task correctness via hidden tests or acceptance checks, and adherence to constraints planted in the sources. Rationale tasks are graded against held-out decision records by rubric, with a blind judge from a different model family plus human spot checks.
- **Secondary:** time, tokens, tool calls, repeated failed approaches, unsupported or stale claims, bootstrap cost, maintenance cost (recorder and consolidator tokens, Stop latency), memory use and disk growth.
- Cost is reported in tokens first, then in a configurable currency with the pricing table and exchange-rate date stated.

### 11.5 Preregistration and kill criteria

A committed `bench/PREREG.md` is fixed before any scored run. It defines the primary metric, a non-inferiority margin against B1, the minimum gain required on memory-specific families, and a bound on cost per solved task including maintenance. Numeric values, and the number of tasks and repetitions needed to detect the minimum gain (a power calculation from the variance run that accounts for clustering of tasks within snapshots and for multiple family comparisons), are set after a small variance run, not guessed in advance. Tuning runs are excluded from scored results.

There are two gates, because before the recorder and consolidator exist W can only be bootstrap retrieval, while the accumulation value the spec relies on needs capture.

- **Gate 1 (after stage 3, bootstrap retrieval only).** Decides whether to keep building. W must show non-inferiority against B1 and a gain on at least one retrieval-dependent family. Failing it stops the programme or sends it back to design. Passing it does not justify a public release.
- **Gate 2 (after increment 1c, with capture and consolidation running).** Adds longitudinal and fresh-session tasks. Success: no loss of correctness, a measurable gain on at least two memory-specific families, and cost within the bound. Fewer searches alone does not count. Failure: if W fails non-inferiority against B1, or gains on fewer than two memory-specific families, Wikory does not get a public release. It stays a private tool or returns to design.
- Results between those outcomes are reported with uncertainty and the decision is the owner's, not the numbers alone.

### 11.6 Staging

1. A retrieval-only evaluation against gold claims (recall@5 and MRR). It involves no agent runs, and it decides the tier 1 trigger.
2. A small agent pilot to measure variance.
3. The full matrix, behind the cost gate (token maths and an estimate, then explicit confirmation).

### 11.7 Harness self-checks

Each grader must fail on a known-wrong solution and pass on the gold one. A negative control feeds the wiki a deliberately stale claim and checks that it is flagged.

### 11.8 Known gap

The benchmark replays single-agent tasks, but the pilot's real workflow is orchestrated. Whether memory reaches workers usefully through orchestrator briefs is untested, and is an open item (14.4).

## 12. Delivery stages

Each stage ends in an exit check, not a date. No schedule or budget is committed.

| Stage | Outcome | Exit check |
|---|---|---|
| 0. Verification spikes | Every unverified capability settled, real payload fixtures captured from Claude Code 2.1.316 and Codex 0.165.0 | A verified-capabilities table with the fixtures committed |
| 1a. Walking skeleton and safety core | `init` (which records the project as trusted), hook decoders, `SessionStart` and `Stop` capture, a deterministic turn record with conditional monotonic writes, `context` and `search` over hand-written pages with FTS5, `--json`, the progress rule, plus the safety core: a minimal claim validator, confined race-resistant file access (5.5 rule 7), owner-only permissions, the manifest-based recoverable publish (section 6) and checkout registration. No model work | Hooks work end to end on Claude Code with a Windows CI job from day one, and tests pass for duplicate Stop events, interruption before the first Stop, malformed input, a stale or switched checkout, symlink and path-escape attempts, and a kill at each publish step |
| 2. Bootstrap | Bootstrap on the pilot with progress UX and the cost gate, refusing projects the user has not trusted | Coverage report, with provenance and unknowns visible |
| 3. Benchmark, gate 1 | `PREREG.md` committed, the retrieval-only evaluation, then the agent pilot, then the full matrix on bootstrap retrieval behind the cost gate | Reproducible findings including full processing overhead. Gate 1: continue building or stop (11.5) |
| 1b. Recorder and validation | Recorder subagent, `record-turn`, the validator, triage | Validator proven to fail on bad input |
| 1c. Consolidation | Consolidator and its job lifecycle, lease epochs and fencing, checkout-bound jobs, the trigger table, link-graph expansion and aliases | Recovery demonstrated with kills at every publish step, and a branch switch during consolidation handled |
| 1d. Trust and hardening | `wikory trust` in full: the hash ledger and transition table, serve-time checks, the TTY grant and the deny hook, Windows ACLs | Fault-injected gates shown to fail |
| 3b. Benchmark, gate 2 | Longitudinal and fresh-session tasks with capture and consolidation running | Gate 2: the kill criteria in 11.5 |
| 4. Other harnesses | Codex adapter, CLI-only contract, desktop-client MCP | Modes verified per harness |
| 4b. Operations features | Encrypted off-device backup and restore, status line wrap, `export claude-memory`, growth control and `wikory compact`, `wikory merge`, the Orca pack (a static document, which may be written earlier) | Each demonstrated against its failure case |
| 5. Release hardening | Signed releases, Homebrew, Scoop and `go install`, documentation, `CODEOWNERS` guidance, `SECURITY.md` and contributing guidelines, the licence and repository move, a trademark register search, and reserving the name on package registries and a domain | Release checklist complete |
| 6. Evidence-gated | Embeddings (tier 1), the CodeGraph provider (tier 2), `wikory serve` and the MCP App | Per the triggers in 7.3 and 8.10 |

The order after the walking skeleton is deliberate: bootstrap and the benchmark come before the rest of the first slice, so the value of baseline retrieval is proved before the most expensive parts are built. Bootstrap writes model-generated claims, so it depends on the safety core in 1a (validation, confinement, permissions, recoverable writes) and on the trust record `init` creates, which is why those sit in 1a and not later. Stages 1b to 1d are planned after the skeleton has shown what the real payloads allow. The operations features in 4b are decided but scheduled after gate 2, so nothing beyond what the benchmark needs is built before a possible stop decision.

**Test approach.** Test first throughout. Hook decoders are tested against real captured payloads. Crash-recovery tests inject kills between file write and database commit. Fault injection is applied to the validator and the trust gate, so each check is shown to fail before it counts. Hook latency is a tested budget. The Windows CI matrix covers hooks and permissions.

**First implementation plan.** Covers stage 0 and increment 1a only. Wikory is used on its own repository from 1a.

**Layout sketch.** `cmd/wikory/`, `internal/` (app, store, capture, validate, retrieve, trust, decode), `plugins/claude/`, `plugins/codex/`, `integrations/`, `bench/` (Python, uv), `docs/`, and `ui/` later.

## 13. Principal risks and responses

| Risk | Response |
|---|---|
| Incorrect summaries become persistent facts | Evidence references, the no-self-evidence rule, explicit uncertainty, semantic review of important changes |
| Outdated knowledge misdirects the agent | Dependency tracking, computed freshness at read time, current-code verification |
| Excessive context or maintenance overhead | Bounded retrieval, deterministic capture, selective synthesis, measured budgets, growth control |
| Branch or concurrent-session contamination | Worktree and revision scope, idempotent ids, single publishing writer, leases |
| Missing turns after crashes or interruption | Durable deterministic capture, checkpoints, recoverable jobs, interrupted stubs |
| Sensitive material leaks into tracked memory | Source scope, exclusions, redaction, publish and promote scans, private and shared separation, server-side push protection |
| Source text injects instructions into future sessions | Evidence-not-instructions framing, sanitisation, per-project trust ledger, least-privilege subagents |
| A hostile repository carries a poisoned wiki | Untrusted until a human grants trust, drift re-assessment, capability requests cannot enable themselves, readers never follow symlinks, committed status re-derived locally |
| A local agent drives a human-only command | Advisory layers only: TTY requirement, a deny hook on agent Bash calls, harness permission prompts, documentation. Not a hard boundary |
| Hook or transcript formats change | Capability checks, versioned decoders, fixtures, visible degraded modes |
| Several hooks interact on the same event | Wikory blocks rarely (only flagged continuations), and the combination behaviour is verified in stage 0 |
| Benchmark rewards the wrong behaviour | Correctness first, controlled source access, total cost including maintenance, preregistration, kill criteria |
| Loss of private turn records | Encrypted off-device backup, promotion through git |

## 14. Open items

### 14.1 Facts to verify in stage 0

- Whether Codex `Stop` can force a continuation, and whether Codex agent definitions enforce tool restrictions.
- Exact Stop payload fields on the installed versions, and transcript lag at stop time.
- How Claude Code combines several Stop hooks that block. Matching hooks run in parallel, but ordering and combination are undocumented. Existing user hooks on the same event make this matter.
- Whether `tools` allowlists are enforced for plugin-defined subagents.
- Plugin packaging and trust behaviour, and whether plugin-bundled platform binaries are viable.
- Whether the Codex sandbox blocks the agent's `wikory` call from writing to the private store.
- Whether a plugin can supply or wrap `statusLine`.
- Whether redirecting `autoMemoryDirectory` can provide write-only memory, and whether the memory file format is stable across versions.
- Whether ChatGPT Desktop supports local stdio MCP.
- Whether the Obsidian reading view hides the claim comments.
- Whether FTS5 is available in the pure-Go SQLite build used.
- Whether Orca worker launches rebuild their generated settings on every launch, and whether an allowlisted plugin's hooks, agents and skills load in a worker.
- Whether the pilot repository is private, which matters for benchmark contamination.
- How a user-typed shell shortcut behaves in each harness. It is not relied on.
- Whether `/clear` fires `SessionStart` with source `clear` on 2.1.316 (the documentation says it does, local notes say it does not).
- Whether subagents share the parent's `session_id`.
- Whether the Bash tool gives processes a pty, which bears on the strength of the TTY check.
- Whether a hook can detach a child process that survives the hook timeout, needed for headless `SessionEnd` and background housekeeping.
- Whether `background` subagents fire `SubagentStop`, and how hook-injected continuation messages appear in the transcript.
- Whether plugin MCP tools are visible to `tools`-allowlisted subagents.
- SQLite WAL behaviour under parallel hook processes on Windows.
- Whether a Wikory `PreToolUse` deny hook can reliably catch `wikory trust` invocations through shell indirection, and what it cannot catch.
- The minimum Go version that provides rooted, traversal-resistant file opens, and their behaviour on Windows reparse points.
- Whether MCP-only subagents can read the transcript slice they need through scoped tools within a workable token budget.

### 14.2 Numbers to set from measurement

The non-inferiority margin and the tier 1 recall margin (a gain of 10 points on recall@5 was only a proposal), the triage heuristics, the consolidation pending threshold, the alias cap, the continuation cap, the notice threshold, retention periods, archive period and page-size cap, and the token estimator. Budgets for Stop-hook latency, per-turn maintenance tokens and storage growth.

### 14.3 Decisions not yet made

- Licence text and repository move: Apache-2.0 is chosen, and the repository moves from the personal account to the POVIEW-AI organisation when and if it is released.
- The name: a trademark register search, package-registry reservation and a domain are needed before release. `wikory.com` is registered by an unknown party. `.dev`, `.app`, `.ai` and `.io` appeared unregistered at the time of the check.
- The frontend framework and how built web assets reach `go install` users.
- The optional trusted-owner allowlist (proposed, to be confirmed in the engine spec).
- Go module path and distribution channels.

### 14.4 Known limits accepted

- Staleness has more false positives until a symbol-level provider exists.
- A plausible but false claim is caught only by review.
- CLI-only mode and Reduced Codex mode have weaker guarantees.
- Time-travel bootstrap needs one bootstrap per snapshot revision, which costs time and money.
- The benefit of memory reaching orchestrated workers through briefs is untested.
- Exported Claude memory notes differ in wording from notes the agent would have written.
- The human-only gates (trust, backup recovery key) are advisory against a hostile local agent. They defend against repository content persuading an agent, not against an attacker who already controls its shell.

### 14.5 Deferred beyond v1

Embeddings, the CodeGraph provider, `wikory serve` and in-browser editing, the interactive MCP App, a native code graph, `wikory integrate` (until a second pack exists), multi-project switching, non-git projects, read-only worker access, and cross-project memory.

### 14.6 Contracts the engine specification must define

The second independent review found these under-specified at architecture level. Each needs a precise contract and acceptance tests in the engine specification.

- **Typed evidence.** Records for blob, commit, test or command result, document and promoted turn, with the full OID plus an algorithm identifier (SHA-1 and SHA-256 repositories), the mapping from `depends_on` to stored hashes, separate committed and working-copy freshness, line-ending conversion handled by comparing like with like (git's own clean-filter view), and conservative outcomes for missing revisions, submodules (unsupported in v1), non-UTF-8 paths and ambiguous renames.
- **Metadata grammar.** A normative grammar for the claim comment: escape order with `%` escaped first, every separator covered (`@`, `#`, `;`, `=`, `"`, space, `>`, double hyphen), a duplicate-field policy, per-line canonicalisation of claim text, and test vectors including malformed comments and Windows paths.
- **Capture state machine.** States, compare-and-swap transitions, immutable versions, the sealing criterion and its timeout, and recovery when transcript pointers disappear or change.
- **Job and publish contracts.** The job table, lease epochs and fencing, the publish manifest schema, reconciliation of old, target and unexpected hashes, and the trigger table as executable tests, including a branch switch during consolidation.
- **Trust transition table** as executable tests, covering dirty files, Wikory's own publishes, Team auto-acceptance and unregistered checkouts.
- **Backup snapshot.** A coordinated snapshot (quiesce publish, use SQLite's online backup, copy turn files consistently), and restore reconciliation that invalidates active leases and re-checks checkout registrations and trust records, tested against concurrent writes and an interrupted publish.

## Appendix A. Decision register

Frozen decisions are numbered F1 to F12. Decisions and changes from the review that followed are numbered G1 to G20.

| ID | Decision |
|---|---|
| F1 | Spec scope is programme architecture across four subsystems; per-subsystem specs follow |
| F2 | Pilot project is claudeclaw-ultra |
| F3 | Full hook adapters for Claude Code and Codex; generic CLI-only contract for other harnesses (v1 scope narrowed by G3) |
| F4 | Wikory is the sole per-project coding memory; other memory systems become bootstrap sources then retire for the project (reversible per 8.9) |
| F5 | Model work runs in-session via subagents on the cheapest adequate model; the working model does no memory work itself |
| F6 | In-session subagent is the default and only universal mode; headless worker is opt-in with observable fallback |
| F7 | Open-source product shipped as Claude Code and Codex plugins, team-enableable |
| F8 | Wiki committed; turns private by default; promotion into `turns/` is the review gate |
| F9 | Go static binary, hooks invoke the binary directly, no shell scripts in the plugin (Windows scope narrowed by G3) |
| F10 | Single shared wiki with claims scoped by branch, worktree and revision (implemented as a per-checkout index with file presence as the scope, G25) |
| F11 | CodeGraph is a benchmark condition; refined: `CodeIndex` seam in v1, provider later |
| F12 | Consolidation fires only from Stop continuations or explicit command; PreCompact writes a checkpoint; SessionStart discloses pending work (recorder cadence revised by G1) |
| G1 | Deterministic turn record at Stop with no model call; recorder continuation only for flagged turns |
| G2 | Build order: 1a, bootstrap, retrieval-only evaluation and pilot, then 1b to 1d (refined by G30: 1a includes the safety core) |
| G3 | v1 is Claude Code on macOS and Linux, Windows beta in CI from day one; Codex and CLI-only after stage 0 |
| G4 | Kill criteria defined; no public release if non-inferiority or two-family gain fails (applied at gate 2, G21) |
| G5 | Apache-2.0; repository moves to POVIEW-AI if released |
| G6 | Binary mints claim metadata; consolidator emits structured proposals |
| G7 | Text hash and human-edit handling |
| G8 | Orchestrator owns memory in orchestrated projects; workers do not load Wikory in v1 |
| G9 | Memory subagents have MCP tools only, with scoped job-bound reads and no generic Read or Bash (revised by G29); MCP registered by default in Claude Code; CLI retained |
| G10 | Stage 0 spike for a write-only auto memory route; export and `preferences[]` as fallback |
| G11 | Orca integration pack: rules snippet plus optional user-installed reminder hook |
| G12 | Two-step install for v1 |
| G13 | Private store keyed by project-id plus origin |
| G14 | Forward-only additive migrations with backup and dry run |
| G15 | Backup and restore, off-device destination required |
| G16 | Status line integration and briefing threshold notice, default on |
| G17 | `integrations/orca/` is static in v1; `wikory integrate` waits for a second pack |
| G18 | Backups encrypted by default; no network code in v1 |
| G19 | Status line is additive to the user's existing one, default on, switchable off |
| G20 | Growth control: page cap, duplicate merging, archive, `wikory compact`, no automatic deletion |
| G21 | Two benchmark gates: gate 1 after stage 3 (bootstrap retrieval, continue or stop), gate 2 after increment 1c (full kill criteria) |
| G22 | Human-only operations are advisory against a hostile local agent; add a `PreToolUse` deny hook layer; slash commands are not claimed to be human-only |
| G23 | Personal trust mode writes its wiki to the private store, never into the repository |
| G24 | `index.md` is not committed; `wikory merge` reconciles conflicted pages by claim id |
| G25 | Per-checkout wiki index; scope by file presence; branch and worktree stay private; evidence identity is the git blob OID |
| G26 | Prefer background subagents for recorder and consolidator, spike first, fall back to deferring consolidation to the next turn |
| G27 | Bootstrap model work runs through in-session batches or the opt-in headless worker; the cost gate reports tokens first |
| G28 | Operations features (backup, status line wrap, export, growth control, merge, Orca pack) are scheduled after gate 2 |
| G29 | Memory subagents get MCP-only scoped, job-bound reads; no generic `Read` (revises G9) |
| G30 | Increment 1a includes the safety core: minimal validator, confined file access, permissions, recoverable publish, checkout registration (refines G2) |
| G31 | Historical replay uses only git-recoverable sources; untracked memory files are evaluated prospectively |
| G32 | Checkout registration, and turns, jobs and publish destinations bound to a checkout (refines G13 and G25) |

## Appendix B. Documented capabilities relied on

Read from live, unversioned documentation on 2026-11-01. None of this has been tested against Claude Code 2.1.316 and each item is confirmed in stage 0.

| Capability | Source | Status |
|---|---|---|
| Stop payload fields including `stop_hook_active`, `last_assistant_message`, `transcript_path`, `background_tasks`, `session_crons`; the transcript may lag | https://code.claude.com/docs/en/hooks | Documented |
| A Stop hook continues the agent with a block decision and reason, or additional context; consecutive-continuation cap | https://code.claude.com/docs/en/hooks | Documented |
| Matching hooks run in parallel; combining several Stop blocks | https://code.claude.com/docs/en/hooks | Combination unverified |
| `SubagentStop` carries `agent_type` and `agent_id` | https://code.claude.com/docs/en/hooks | Documented |
| `PreCompact` can block; context injection undocumented. `SessionEnd` cannot block, default budget 1.5 seconds | https://code.claude.com/docs/en/hooks | Documented |
| `SessionStart` sources `startup`, `resume`, `clear`, `compact`, `fork`; injected context capped at 10,000 characters | https://code.claude.com/docs/en/hooks | Documented |
| Plugin hooks in `hooks/hooks.json`, `${CLAUDE_PLUGIN_ROOT}`, exec form needs a real executable on Windows | https://code.claude.com/docs/en/plugins/components | Documented |
| Plugin agents: `model`, `tools`, `disallowedTools` supported; `hooks`, `permissionMode`, `mcpServers` ignored | https://code.claude.com/docs/en/sub-agents | Enforcement of `tools` unverified |
| Forcing the main agent to spawn a named subagent from a hook | https://code.claude.com/docs/en/sub-agents | Not a documented pattern |
| `autoMemoryEnabled` and `autoMemoryDirectory`; no read-only or write-only mode | https://code.claude.com/docs/en/memory | Documented |

## Appendix C. Independent review and dispositions

A separate model reviewed draft 1 with no access to the design discussion and reported 21 findings. The owner approved the decisions G21 to G28 that came out of it. Dispositions:

| Finding | Disposition |
|---|---|
| The release gate runs before capture exists | Two gates (11.5, G21) |
| Bootstrap model work not reconciled with F5 and F6 | Section 9, G27 |
| One shared database against repo-wide scope | Per-checkout index (4.4, 7.1, G25) |
| Revision identity breaks under rebase, squash, force push and shallow clones; dirty-tree evidence | Blob OID identity and committed evidence only (5.5 rule 2) |
| A TTY check is not a human-only boundary, and slash commands run through the agent | Stated as advisory, deny hook layer (10.4, 8.3, G22) |
| Symlinks in an attacker-controlled repository | No-follow rule (5.5 rule 7, 10.3) |
| Forgeable authorship and unauthenticated committed status | Re-derived locally (5.5 rule 6, 10.4) |
| Text hash against CRLF and normalisation, comment encoding | Canonical form and encoding (5.3) |
| The deterministic floor depends on transcript timing | Two-phase capture with a sealed state (6) |
| The Stop continuation makes the user wait | Background dispatch preferred, fallback defined (6, G26) |
| Self-exclusion keyed on session, job row before spawn | Job lifecycle keyed on agent id and nonce (6) |
| Publish cannot re-spawn a job | Retry queued for a later continuation (6) |
| B1 contamination, planted constraints, statistical power | 11.1, 11.2, 11.5 |
| Branch and worktree in committed metadata | Private store only (4.4, 5.3, G25) |
| Personal mode still produces diffs | Private-store wiki (10.4, G23) |
| A custom FTS5 tokeniser may not be possible | Expanded text column with a built-in tokeniser (7.1) |
| `SessionStart` source `clear`, startup scan budget | Sections 6 and 7, 14.1 |
| Append conflicts and `index.md` | `index.md` not committed, `wikory merge` (4.2, 4.5, G24) |
| Operations features built before the kill decision | Stage 4b (12, G28) |
| Store key stability, recovery key in transcripts, `forget` limits, network wording, `PreCompact` as a boundary | 4.3, 4.7, 10.6, 1.2, 6 |

Facts the reviewer could not verify were added to the stage 0 list (14.1).

## Appendix D. Second independent review and dispositions

A second reviewer (GPT-6 Astra, through a read-only Codex task) reviewed draft 2 and reported 14 findings: 8 High and 6 Medium, no Critical. This was a document review with no runtime tests. The owner approved decisions G29 to G32 that came out of it. Dispositions:

| Finding | Disposition |
|---|---|
| Store identity is not a trust boundary | Checkout registration (4.3, G32) |
| Trust drift rules contradict each other | One transition table (10.4) |
| Checkout isolation stops at the index | Checkout binding of turns, jobs and publish (4.4, G32) |
| Publication is not recoverable across all steps | Manifest protocol and lease epoch fencing (6) |
| Page hashes do not protect against a changing checkout | Tree fingerprint and revalidation before publish (6) |
| Read-only subagents can still disclose private information | MCP-only scoped reads (8.2, 10.3, G29) |
| No-follow protection under-specified and overbroad | Authorised roots and race-resistant opens (5.5 rule 7) |
| Safe bootstrap depends on later components | Safety core in 1a (12, G30) |
| Background fallback and consolidation triggers conflict | Trigger table (6) |
| Capture idempotency and sealing incomplete | Conditional monotonic writes, sealing criterion (6) |
| Evidence identity lacks a typed contract | Engine specification contract (14.6) |
| Metadata encoding not demonstrably reversible | Engine specification contract (14.6) |
| Benchmark parity and historical isolation incomplete | Two comparison types, recoverable sources, per-run namespaces (11.1, 11.2, 11.5, G31) |
| Backup consistency unspecified | Engine specification contract (14.6) |
