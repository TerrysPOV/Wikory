# Project Wiki Memory for Coding Agents

**High-level project proposal · 31 October 2026 · Version 0.1**  
**Status:** Proposal for further discovery and design in Claude Code. This is not an approved implementation specification. No prototype or comparative benchmark has been completed.

## 1. Genesis of the idea

This proposal originated in Terry’s observation that coding agents repeatedly reconstruct project knowledge that previous sessions have already established. Inspired by Andrej Karpathy’s LLM Wiki pattern, Terry proposed giving each coding project a persistent, agent-maintained wiki: project documents would be accessible through a `raw/` folder, including symlinks to their original locations; each completed turn would produce a summary; related documents and code would be cross-linked; and an evolving index would help the next agent orient itself before searching the codebase. The initial discussion extended this idea into a shared memory capability for Claude Code and Codex, with a controlled comparison against Terry’s existing CodeGraph setup. A central requirement is making the approach useful for existing projects that have memory files and handoff notes but little or no structured turn history.

## 2. Purpose and expected value

Build durable, inspectable project memory that preserves knowledge across sessions, context compaction and changes of coding agent. The intended benefit is less repeated investigation, better continuity and fewer mistakes caused by forgotten constraints or previously rejected approaches.

The wiki should preserve information that is difficult to recover from code alone: why a decision was made, which alternatives failed, what a user explicitly required, what was verified, and what remains unfinished. It should also provide a concise map of the implementation, with traceable references to the current code.

**Operating principle: consult memory to orient the work, then verify relevant claims against current evidence.** The wiki guides `rg`, LSP, direct file inspection and optional CodeGraph queries; it does not replace them or become an unquestioned source of implementation truth.

The expected productivity improvement is a hypothesis to test, not a measured result. Recording and maintaining memory also consumes time and model tokens, which must be included in the evaluation.

## 3. Objectives and initial scope

### Objectives

- Share one project knowledge base between Claude Code and Codex.
- Capture each turn reliably, including useful partial work from interrupted turns.
- Accumulate durable knowledge rather than merely archive conversation summaries.
- Preserve provenance, uncertainty, contradictions and superseded decisions.
- Keep retrieval bounded and relevant to the current task and checkout.
- Bootstrap existing projects without fabricating historical conversations or rationale.
- Compare the wiki with CodeGraph, and test whether combining them adds value.
- Keep the initial implementation suitable for a MacBook Air M2 with 16 GB RAM.

### Initial scope

Start with one local Git project and the locally installed Claude Code and Codex surfaces. Use readable Markdown, a small local indexing and processing layer, and thin adapters for each harness. Obsidian should be an optional reader rather than a required runtime dependency.

Hosted environments, multi-user permissions, organisation-wide knowledge, large-scale semantic retrieval and automatic cross-project learning are later decisions. A local symlink does not make a source available to a cloud agent; remote operation requires a separate source-access design.

## 4. Approaches considered

| Approach | Benefits | Limitations |
| --- | --- | --- |
| Markdown and agent instructions only | Fast to trial; few dependencies; easy to inspect | Capture, validation and maintenance rely heavily on agent compliance |
| Shared memory engine with harness adapters | Consistent behaviour, reliable capture and portable storage | Requires a small CLI or service and explicit lifecycle handling |
| Wiki tightly integrated into CodeGraph | May combine structural navigation and project rationale | Depends on the exact CodeGraph implementation and makes independent evaluation harder |

**Recommended direction:** a shared memory engine, with CodeGraph behind an optional adapter. This recommendation remains provisional pending inspection of the existing tools and project.

The suggested initial stack is Python managed with `uv`, Markdown as the readable knowledge layer, and SQLite for search, link relationships, ingestion state and job tracking. Start with lexical retrieval and explicit links. Add embeddings, reranking or a separate graph store only if the evaluation establishes a material gap.

## 5. Proposed architecture

The system separates source evidence, chronological experience and consolidated project knowledge.

| Layer | Responsibility | Suggested representation |
| --- | --- | --- |
| Source registry | Identify documents, code revisions and permitted source locations | Source IDs, locations, hashes and metadata |
| Raw evidence | Preserve or reference the material supporting a claim | `project-memory/raw/`, Git references and selected snapshots |
| Turn history | Record what was requested, attempted, observed and left unresolved | Structured records in `project-memory/turns/` |
| Project wiki | Maintain current topic-level synthesis with evidence links | Markdown in `project-memory/wiki/` |
| Navigation | Provide concise entry points and chronological maintenance history | `index.md` and `log.md` |
| Local engine | Retrieve, validate, reconcile, queue and coordinate writes | CLI, SQLite and optional local MCP interface |
| Harness adapters | Translate lifecycle events into the common memory contract | Claude Code and Codex integrations |

Markdown is the inspectable knowledge representation. SQLite holds operational state and derived indexes; it must not be the sole home of irreplaceable project knowledge. Rebuildable indexes and durable pending capture records need separate recovery policies.

A project-wide installation should reuse one engine rather than run a heavyweight stack for every repository. Model calls may use an existing authenticated harness or an explicitly configured provider; that choice, its billing implications and its execution permissions remain open.

### Source documents and symlinks

Use project-relative `project-memory/raw/`, not an absolute filesystem `/raw` directory. Documents can remain in their existing authoritative locations and be registered or linked into this folder.

A symlink is a navigation convenience, not an immutable snapshot or a write restriction. The ingestion process should treat sources as read-only and record a stable source ID, resolved location, content hash, ingestion time and version information. For reproducibility, preserve the version used for synthesis through Git references or selected snapshots.

Resolve only registered locations, detect loops and deduplicate identical content. Record moved or unavailable targets. Recreate machine-specific links from local configuration rather than commit absolute paths. Extracted text from PDFs or office documents is a derived representation and should retain a reference to its original source.

### Project identity and storage

Assign a stable project identity, with separate identities for worktrees and sessions. Shared repository knowledge and branch-specific observations must remain distinguishable.

The proposed default is to version suitable wiki content and configuration with the project, while keeping private transcripts, machine-specific paths, SQLite files, locks and job state outside tracked content. Whether turn summaries should be committed, selectively promoted or kept in a separate private store requires a deliberate decision before implementation.

## 6. Turn capture and wiki maintenance

### What constitutes a turn

A turn begins with a user request and includes the resulting agent activity until completion or interruption. Tool calls, progress messages and hook-induced continuations should not each create independent user turns. Subagent work should attach to its parent task, retaining attribution where useful.

Every turn should have a durable record. A turn with no substantive new information can produce a minimal record and no wiki changes.

### Proposed turn record

- User request and relevant constraints.
- Outcome and status: completed, partial, interrupted or failed.
- Decisions and explicitly stated rationale.
- Files and symbols inspected or changed, with supporting evidence where available.
- Verification commands, observed results and limitations.
- Failed approaches and what was learned.
- Outstanding questions and next steps.
- Project, harness, session and turn identifiers.
- Worktree identity, Git revision and relevant uncommitted-file fingerprints.
- Source references and processing status.

Record observable work and stated rationale; the design does not depend on access to private model reasoning. Distinguish the agent’s assertion that a test passed from an observed successful test result.

### Processing lifecycle

1. **Capture:** persist an event and evidence references quickly at the turn boundary.
2. **Summarise:** accept a structured summary from the working agent while its task context is available.
3. **Recover if needed:** queue a fallback summary from available conversation and tool records when the working agent omitted one.
4. **Validate:** check schema, references and evidence; separate supported observations from unverified claims.
5. **Consolidate:** update only affected wiki topics, recording contradictions and supersession.
6. **Index:** regenerate navigation and backlinks from structured relationships.

Summarisation and consolidation require model work; hooks alone do not provide them. Use bounded jobs and measure their overhead. Deterministic checks can validate links and hashes, but cannot guarantee the semantic truth of a claim.

Use idempotent event IDs, atomic writes and a single publishing writer per project. Candidate changes should be checked against the current page version before publication. Exclude the memory worker’s own activity from normal capture to prevent recursive updates.

Persist captures before expensive processing. If a worker crashes, the queue must be recoverable. A failed memory update should expose its status without unnecessarily blocking coding work or falsely reporting that memory is current.

## 7. Evidence, freshness and backlinks

### Distinguish different kinds of knowledge

| Knowledge type | Evidence | Treatment |
| --- | --- | --- |
| Current implementation | Code, configuration and observed behaviour | Recheck when relevant sources change |
| Intended behaviour | Requirements and explicit user decisions | Preserve even when implementation differs |
| Historical rationale | Attributable discussions, notes and decision records | Retain its source and historical context |
| Hypothesis | An inference or proposed explanation | Label as unverified until supported |

Recent text does not automatically outrank older authoritative evidence. A requirement describes intent; it does not prove implementation. An agent-generated summary must never be its own sole evidence for a durable claim.

Wiki pages should carry source references, applicability scope, verification metadata and status. Where a page contains mixed evidence, retain claim-level references rather than assigning one blanket confidence label.

Track dependencies from claims and pages to documents, files and symbols. Changes should mark affected knowledge as potentially stale. Hashes establish that content changed, not whether a claim became false. Reconcile affected knowledge before relying on it, including changes made outside the coding agent.

A recent successful check is evidence of verification at that time, not a guarantee of continuing correctness. Renames, deletions, merges and branch switches need explicit handling.

### Links to code

Use repository-relative paths, symbol names and revision references. Treat line numbers as a convenience rather than a stable identity. Companion wiki pages for files or components can collect incoming references without modifying production source code to insert backlinks.

Generate backlinks deterministically from forward relationships. Keep the top-level index compact, with topic indexes or search as the corpus grows. Preserve historical records while consolidating repeated lessons into concise topic pages.

## 8. Claude Code and Codex integration

Both agents should use the same underlying schema and operations. Keep `CLAUDE.md` and `AGENTS.md` short and point them to a common memory contract.

| Proposed operation | Purpose |
| --- | --- |
| `context` | Produce a bounded briefing for the task and current checkout |
| `search` | Retrieve decisions, lessons, components and source evidence |
| `record-turn` | Submit an attributed structured turn record |
| `refresh` | Reconcile changed sources and affected knowledge |
| `doctor` | Report missing captures, stale claims, broken links and contradictions |

These names describe a proposed interface; they are not existing commands.

Use session start/resume for orientation, new-request events for task retrieval, turn-stop events for capture, and available compaction/interruption events for checkpoints. Detect installed versions and capabilities before selecting the adapter behaviour.

Claude Code documentation describes relevant lifecycle hooks and supplies a final assistant message in the stop payload. Its documentation also warns that the transcript may not yet include that final message at stop time. Codex documentation describes similar lifecycle hooks but explicitly treats its transcript format as unstable. Prefer structured event fields; isolate any necessary transcript parsing behind tested, versioned adapters.

Hooks must remain fast and avoid continuation loops. Where hooks are unavailable, use an explicit command or launcher integration and disclose its reduced capture guarantees. Plugin packaging, trust setup and host-specific restrictions need validation on the installed surfaces.

### Retrieval and consistency

Begin with a proposed orientation budget of approximately 1,000–2,000 tokens, then retrieve selected pages for the task. This is an initial tuning target, not a measured optimum. Consult memory again at useful task boundaries, such as encountering a known failure signature or entering a different subsystem.

Background consolidation creates a consistency gap: the next request may arrive before the wiki is updated. The retrieval operation should expose the last processed turn and pending work, then either include pending summaries or wait briefly for relevant processing. It must not silently present an older wiki as fully current.

## 9. Bootstrapping existing projects

Create an explicitly labelled current-state baseline. Do not invent historical turns or infer a definite rationale where none is recorded.

1. Inventory the current checkout, documents, memory files, handoffs, decision records and available Git history.
2. Register sources with provenance and applicable dates or revisions.
3. Extract existing claims while preserving attribution and uncertainty.
4. Inspect code, configuration and relevant tests to establish current implementation.
5. Create baseline pages for purpose, architecture, major components, constraints, known issues and unfinished work.
6. Mark findings as verified, historical, conflicting, unverified or unknown.
7. Start genuine turn capture from that point forward and expand coverage as work touches new areas.

A handoff stating that a migration is complete is an attributed claim until checked. A commit history may establish what changed without establishing why. If rationale is unavailable, say so.

Prioritise active or important areas rather than generating documentation for every function. Produce a bootstrap coverage report showing what was inspected, what was verified and what remains unknown.

## 10. Evaluation against CodeGraph

The exact CodeGraph repository, package, version and current configuration have not been identified. Inspect them before asserting capabilities or designing a concrete adapter.

The evaluation should test four configurations:

| Configuration | Available assistance |
| --- | --- |
| Baseline | Existing instructions, direct reads, `rg` and LSP |
| CodeGraph | Baseline plus the installed CodeGraph capability |
| Wiki | Baseline plus project wiki memory |
| Hybrid | Baseline plus both systems |

Run comparisons within each harness, holding model, settings, task, starting revision and resource budget constant. A comparison between different models or harnesses cannot isolate the effect of the memory method.

Keep ordinary source access equivalent. When testing a particular retrieval mechanism, isolate the additional index or memory tool rather than inadvertently giving one condition extra source documents. Separately report the practical benefit of accumulated turn history when that is unique to the wiki condition.

### Tasks and measures

Include implementation discovery, dependency tracing, rationale recovery, bug fixing, constrained feature changes, fresh-session continuation and recovery from stale notes or renamed files.

Prioritise correctness, task-specific verification and constraint adherence. Also measure elapsed time, tokens, tool calls, repeated failed approaches, unsupported or stale claims, bootstrap cost, maintenance cost, memory use and disk growth. If monetary costs are reported, use GBP with explicit pricing and exchange-rate assumptions.

Use isolated checkouts, conversations and memory stores; repeat tasks and vary execution order. Reset memory between independent runs. Within a longitudinal test, allow accumulation only along that test’s prescribed sequence. Hold out solutions and later notes, especially when replaying historical tasks.

Test both newly bootstrapped memory and memory accumulated across a controlled sequence of sessions. The latter is where retaining project experience should provide distinctive value.

Define success thresholds before running the evaluation. The primary requirement is improved usefulness without reduced correctness; fewer searches alone is not a sufficient success metric. Report quality–cost trade-offs and uncertainty rather than selecting a winner from a single run.

## 11. Delivery stages

| Stage | Main outcome | Exit condition |
| --- | --- | --- |
| Discovery | Inspect one pilot project, both harnesses and CodeGraph | Capabilities, constraints and pilot tasks documented |
| Baseline | Evidence-backed starter wiki | Provenance and coverage are visible; unknowns remain labelled |
| Capture prototype | Reliable turn records through both adapters | Normal, interrupted, duplicate and recovery cases demonstrated |
| Retrieval and synthesis | Bounded context, affected-page updates and freshness checks | Relevant memory is retrieved and stale evidence is surfaced |
| Comparative evaluation | Baseline, CodeGraph, wiki and hybrid results | Reproducible findings with full processing overhead |
| Hardening decision | Decide whether and how to expand | Benefits justify maintenance, packaging and operational effort |

No schedule or budget is committed at this stage. Estimate effort after discovery establishes the integration surface and CodeGraph implementation.

## 12. Principal risks and mitigations

| Risk | Proposed response |
| --- | --- |
| Incorrect summaries become persistent facts | Evidence references, explicit uncertainty and semantic review of important changes |
| Outdated knowledge misdirects the agent | Dependency tracking, freshness checks and current-code verification |
| Excessive context or maintenance overhead | Bounded retrieval, selective synthesis and measured budgets |
| Branch or concurrent-session contamination | Worktree/revision scope, idempotency and coordinated publication |
| Missing turns after crashes or interruption | Durable capture, checkpoints and recoverable jobs |
| Sensitive material leaks into tracked memory | Explicit source scope, exclusions and private/shared storage separation |
| Source text injects instructions into future sessions | Treat retrieved content as evidence, not executable instructions; keep the trusted contract separate |
| Hook or transcript formats change | Capability checks, versioned adapters and visible degraded modes |
| Benchmark rewards the wrong behaviour | Score correctness first; control source access and include total cost |

## 13. Open decisions for Claude Code

1. Which CodeGraph implementation and version are installed, and which capabilities are actually used?
2. Which repository is the best pilot, and what representative tasks can be evaluated independently?
3. Which local Claude Code and Codex versions and surfaces must be supported first?
4. Which memory files, handoffs and documents are authoritative enough to ingest?
5. Where should private turns, source snapshots and shared wiki pages live, and what should be committed?
6. Should normal synthesis run inside the active agent, through a separate harness worker, or through a configured model API?
7. What latency, token and storage budgets are acceptable, and which operations need stronger consistency?
8. How should branch-specific findings be reconciled after merges or discarded experiments?
9. What permissions and review rules should govern promotion of generated knowledge into shared project documentation?
10. What benchmark thresholds would justify adopting the wiki, keeping CodeGraph, or using both?

## 14. Suggested continuation in Claude Code

Use this document as a discovery brief rather than instructions to implement every proposed detail. Inspect the pilot repository, existing memory practices and actual CodeGraph installation first. Verify lifecycle support on both installed harnesses. Then refine the smallest architecture that can test the hypothesis, settle the material storage and synthesis decisions, and produce an implementation plan with a reproducible evaluation protocol.

Preserve the original intent: each project should become easier for a fresh agent to understand because useful knowledge accumulates over time. Keep that goal distinct from producing more documentation or minimising code inspection at any cost.

## 15. References and evidence status

The following primary sources were reviewed during the architecture discussion on 31 October 2026. Recheck them against installed versions during implementation. This proposal does not claim that the integrations have been tested locally.

- [Andrej Karpathy — LLM Wiki concept](https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f): source/wiki/schema separation, synthesis, indexing and maintenance.
- [Claude Code — Hooks reference](https://code.claude.com/docs/en/hooks): lifecycle integration, stop-event payloads and transcript timing caveats.
- [OpenAI — Hooks](https://learn.chatgpt.com/docs/hooks): Codex lifecycle events, configuration and transcript-interface limitations.
- [OpenAI — Package your plugin](https://developers.openai.com/plugins/build/plugins): plugin packaging and lifecycle-hook distribution and trust considerations.

The shared engine, data model, storage layout, processing contract, bootstrap workflow and benchmark design are proposed adaptations for this project. They are not features supplied by Karpathy’s concept document and are not yet validated implementation choices.
