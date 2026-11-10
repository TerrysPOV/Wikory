package store

// SchemaVersion of wikory.db. A newer database makes this binary refuse to open it (spec 4.6).
const SchemaVersion = 1

const schemaSQL = `
CREATE TABLE IF NOT EXISTS meta(k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS checkouts(
  id TEXT PRIMARY KEY, common_dir TEXT NOT NULL, worktree TEXT NOT NULL, registered_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS trust(
  common_dir TEXT PRIMARY KEY, decision TEXT NOT NULL, reason TEXT NOT NULL, at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS counters(name TEXT PRIMARY KEY, n INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS turn_starts(
  session_id TEXT NOT NULL, marker TEXT NOT NULL, checkout_id TEXT NOT NULL,
  transcript_path TEXT NOT NULL, transcript_offset INTEGER NOT NULL,
  request TEXT NOT NULL, at TEXT NOT NULL,
  PRIMARY KEY(session_id, marker));
CREATE TABLE IF NOT EXISTS turns(
  turn_id TEXT PRIMARY KEY, checkout_id TEXT NOT NULL, session_id TEXT NOT NULL, marker TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('captured','sealed')),
  version INTEGER NOT NULL CHECK(version >= 1),
  outcome TEXT NOT NULL, stub INTEGER NOT NULL DEFAULT 0,
  request TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS turns_by_checkout ON turns(checkout_id, state);
CREATE TABLE IF NOT EXISTS jobs(
  job_id TEXT PRIMARY KEY, checkout_id TEXT NOT NULL, kind TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('begun','applying','done','conflict','abandoned','refused')),
  epoch INTEGER NOT NULL, fingerprint TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS job_ops(
  job_id TEXT NOT NULL, idx INTEGER NOT NULL, target TEXT NOT NULL,
  old_hash TEXT NOT NULL, new_hash TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('pending','done')),
  PRIMARY KEY(job_id, idx));
CREATE TABLE IF NOT EXISTS pages(
  checkout_id TEXT NOT NULL, relpath TEXT NOT NULL, content_hash TEXT NOT NULL,
  page_id TEXT NOT NULL, title TEXT NOT NULL, type TEXT NOT NULL,
  PRIMARY KEY(checkout_id, relpath));
CREATE TABLE IF NOT EXISTS claims(
  checkout_id TEXT NOT NULL, relpath TEXT NOT NULL, ordinal INTEGER NOT NULL,
  claim_id TEXT NOT NULL, meta TEXT NOT NULL, text TEXT NOT NULL, heading TEXT NOT NULL,
  status TEXT NOT NULL, kind TEXT NOT NULL,
  PRIMARY KEY(checkout_id, relpath, ordinal));
CREATE TABLE IF NOT EXISTS page_notes(
  checkout_id TEXT NOT NULL, relpath TEXT NOT NULL, malformed INTEGER NOT NULL, degraded TEXT NOT NULL,
  PRIMARY KEY(checkout_id, relpath));
CREATE VIRTUAL TABLE IF NOT EXISTS fts USING fts5(
  title, heading, claim, body, search_text,
  checkout_id UNINDEXED, relpath UNINDEXED, ordinal UNINDEXED,
  tokenize = 'unicode61');
CREATE TABLE IF NOT EXISTS bs_runs(
  run_id TEXT PRIMARY KEY, checkout_id TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('planned','sampling','awaiting-confirm','running','finishing','done','abandoned')),
  plan_hash TEXT NOT NULL, scope TEXT NOT NULL, head_commit TEXT NOT NULL,
  sample INTEGER NOT NULL DEFAULT 0, end_marker TEXT NOT NULL DEFAULT '',
  accepted INTEGER NOT NULL DEFAULT 0, rejected INTEGER NOT NULL DEFAULT 0, roots TEXT NOT NULL DEFAULT '',
  tokens_in INTEGER NOT NULL DEFAULT 0, tokens_out INTEGER NOT NULL DEFAULT 0,
  sample_in INTEGER NOT NULL DEFAULT 0, sample_out INTEGER NOT NULL DEFAULT 0,
  sample_acc INTEGER NOT NULL DEFAULT 0, sample_rej INTEGER NOT NULL DEFAULT 0,
  coverage TEXT NOT NULL DEFAULT '', branch TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS bs_runs_by_checkout ON bs_runs(checkout_id, state);
CREATE TABLE IF NOT EXISTS bs_sources(
  run_id TEXT NOT NULL, checkout_id TEXT NOT NULL, source TEXT NOT NULL, path TEXT NOT NULL,
  oid TEXT NOT NULL, tier TEXT NOT NULL, hash TEXT NOT NULL, size INTEGER NOT NULL,
  disposition TEXT NOT NULL, secret_spans INTEGER NOT NULL DEFAULT 0, secret_lines TEXT NOT NULL DEFAULT '',
  src_date TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(run_id, source, path));
CREATE TABLE IF NOT EXISTS bs_components(
  run_id TEXT NOT NULL, checkout_id TEXT NOT NULL, name TEXT NOT NULL, prefix TEXT NOT NULL,
  score REAL NOT NULL, rank INTEGER NOT NULL, inspected INTEGER NOT NULL,
  files INTEGER NOT NULL DEFAULT 0, entry INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(run_id, name));
CREATE TABLE IF NOT EXISTS bs_items(
  item_id TEXT PRIMARY KEY, run_id TEXT NOT NULL, checkout_id TEXT NOT NULL,
  stage TEXT NOT NULL CHECK(stage IN ('extract','verify')), ref TEXT NOT NULL, input_hash TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('pending','leased','done','rejected')),
  epoch INTEGER NOT NULL DEFAULT 0, attempts INTEGER NOT NULL DEFAULT 0, sampled INTEGER NOT NULL DEFAULT 0,
  batch TEXT NOT NULL DEFAULT '', leased_at TEXT NOT NULL DEFAULT '', done_at TEXT NOT NULL DEFAULT '',
  tokens_in INTEGER NOT NULL DEFAULT 0, tokens_out INTEGER NOT NULL DEFAULT 0,
  confirmed INTEGER NOT NULL DEFAULT 0, contradicted INTEGER NOT NULL DEFAULT 0, undetermined INTEGER NOT NULL DEFAULT 0,
  UNIQUE(run_id, stage, ref));
CREATE INDEX IF NOT EXISTS bs_items_by_run ON bs_items(run_id, stage, state);
CREATE TABLE IF NOT EXISTS bs_claims(
  run_id TEXT NOT NULL, checkout_id TEXT NOT NULL, item_id TEXT NOT NULL, ordinal INTEGER NOT NULL,
  claim_id TEXT NOT NULL, text TEXT NOT NULL, kind TEXT NOT NULL, status TEXT NOT NULL,
  meta TEXT NOT NULL, tier TEXT NOT NULL, contra TEXT NOT NULL DEFAULT '', loc TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(run_id, item_id, ordinal));
CREATE TABLE IF NOT EXISTS bs_pages(
  checkout_id TEXT NOT NULL, path TEXT NOT NULL, hash TEXT NOT NULL, run_id TEXT NOT NULL,
  pending TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(checkout_id, path));
`
