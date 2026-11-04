package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/gitx"
	"github.com/TerrysPOV/Wikory/internal/store"
)

// bootstrapRepo is an initialised, trusted repository whose HEAD holds some code, a doc and a
// handoff, all committed. project-memory/ is committed too, as a person would after init.
func bootstrapRepo(t *testing.T) (string, *cli) {
	t.Helper()
	dir := newGitRepo(t)
	files := map[string]string{
		"internal/queue/queue.go":                 "package queue\n\n// Ack acknowledges after the handler returns.\nfunc Ack() {}\n",
		"docs/design.md":                          "The internal/queue package acknowledges after the handler returns.\n",
		".claude/handoffs/PRIOR_SESSION_STATE.md": "Open item: internal/queue retry.\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := newCLI(t)
	if _, e, code := c.run(dir, "init"); code != ExitOK {
		t.Fatalf("init: %s", e)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "baseline")
	return dir, c
}

type planData struct {
	RunID      string         `json:"run_id"`
	PlanHash   string         `json:"plan_hash"`
	Scope      string         `json:"scope"`
	Head       string         `json:"head"`
	Sample     int            `json:"sample"`
	Sources    map[string]int `json:"sources"`
	Components int            `json:"components"`
	Inspected  int            `json:"inspected"`
	Batches    int            `json:"batches"`
	Estimates  []struct {
		Stage        string `json:"stage"`
		Items        int    `json:"items"`
		InputTokens  int    `json:"input_tokens"`
		OutputTokens int    `json:"output_tokens"`
	} `json:"estimates"`
}

func runBootstrapJSON(t *testing.T, c *cli, dir string, args ...string) (Envelope, int, string) {
	t.Helper()
	out, errOut, code := c.run(dir, append([]string{"--json", "bootstrap"}, args...)...)
	var e Envelope
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatalf("not an envelope (code %d): %v\nstdout=%s\nstderr=%s", code, err, out, errOut)
	}
	return e, code, errOut
}

func decodeData[T any](t *testing.T, e Envelope) T {
	t.Helper()
	b, _ := json.Marshal(e.Data)
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBootstrapPlanReportsAndStoresARun(t *testing.T) {
	dir, c := bootstrapRepo(t)
	e, code, _ := runBootstrapJSON(t, c, dir, "plan")
	if code != ExitOK || !e.OK || e.Schema != "wikory.bootstrap/v1" {
		t.Fatalf("code=%d %+v", code, e)
	}
	d := decodeData[planData](t, e)
	if !strings.HasPrefix(d.RunID, "run_") || len(d.PlanHash) != 64 || d.Head != gitIn(t, dir, "rev-parse", "HEAD") {
		t.Fatalf("%+v", d)
	}
	if d.Sources["queued"] < 3 || d.Components < 1 || len(d.Estimates) != 2 || d.Batches < 1 {
		t.Fatalf("%+v", d)
	}
}

func TestBootstrapPlanIsDeterministicButMakesANewRun(t *testing.T) {
	dir, c := bootstrapRepo(t)
	a, _, _ := runBootstrapJSON(t, c, dir, "plan")
	b, _, _ := runBootstrapJSON(t, c, dir, "plan")
	da, db := decodeData[planData](t, a), decodeData[planData](t, b)
	if da.PlanHash != db.PlanHash || da.RunID == db.RunID {
		t.Fatalf("same input, same hash; each call its own run: %+v %+v", da, db)
	}
}

func TestBootstrapPlanRefusesAFeatureBranchAndADirtyTree(t *testing.T) {
	dir, c := bootstrapRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "project-memory", "wiki"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "project-memory", "wiki", "scratch.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := runBootstrapJSON(t, c, dir, "plan"); code != ExitOK {
		t.Fatal("an uncommitted page under project-memory/ must not block a plan")
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "queue", "queue.go"), []byte("package queue\n// edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "plan")
	if code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, "uncommitted") {
		t.Fatalf("dirty tree: code=%d %+v", code, e)
	}
	gitIn(t, dir, "checkout", "-q", "--", "internal/queue/queue.go")
	gitIn(t, dir, "checkout", "-q", "-b", "feat/x")
	e, code, _ = runBootstrapJSON(t, c, dir, "plan")
	if code != ExitUsage || !strings.Contains(e.Error.Message, "default branch") {
		t.Fatalf("feature branch: code=%d %+v", code, e)
	}
}

func TestBootstrapPlanNeverPrintsSecretText(t *testing.T) {
	dir, c := bootstrapRepo(t)
	secret := "hunter2hunter2"
	body := "package queue\n\n// pass" + "word=" + secret + "\n"
	if err := os.WriteFile(filepath.Join(dir, "internal", "queue", "cfg.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "add config")
	out, errOut, code := c.run(dir, "--json", "bootstrap", "plan")
	if code != ExitOK || strings.Contains(out+errOut, secret) {
		t.Fatalf("code=%d; the secret must never reach stdout or stderr\n%s\n%s", code, out, errOut)
	}
	var e Envelope
	_ = json.Unmarshal([]byte(out), &e)
	if decodeData[planData](t, e).Sources["skipped:secret"] != 1 {
		t.Fatalf("the file with a secret should be counted as skipped: %s", out)
	}
}

func TestBootstrapPlanListsUnmergedBranchesAndOtherWorktreesAsNotRead(t *testing.T) {
	dir, c := bootstrapRepo(t)
	gitIn(t, dir, "checkout", "-q", "-b", "feature/x")
	if err := os.WriteFile(filepath.Join(dir, "later.go"), []byte("package later\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "work that only the branch has")
	gitIn(t, dir, "checkout", "-q", "main")
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	gitIn(t, dir, "worktree", "add", "-q", filepath.Join(parent, "side"), "feature/x")
	e, code, _ := runBootstrapJSON(t, c, dir, "plan")
	d := decodeData[planData](t, e)
	if code != ExitOK || d.Sources["skipped:unmerged-branch"] != 1 || d.Sources["skipped:other-worktree"] != 1 {
		t.Fatalf("an unmerged branch and another worktree are both listed as not read: code=%d %+v", code, d.Sources)
	}
	out, _, _ := c.run(dir, "bootstrap", "plan")
	if !strings.Contains(out, "Not read: 1 branch(es) with unmerged commits and 1 other worktree(s)") {
		t.Fatalf("the plan text must say work exists outside the baseline:\n%s", out)
	}
	again, _, _ := runBootstrapJSON(t, c, dir, "plan")
	if decodeData[planData](t, again).PlanHash != d.PlanHash {
		t.Fatal("the same repository state gives the same plan hash")
	}
	// Local branches and worktrees are listed but not hashed: another clone of the same commit
	// has others (final review A3).
	gitIn(t, dir, "branch", "feature/y", "feature/x")
	moved, _, _ := runBootstrapJSON(t, c, dir, "plan")
	if m := decodeData[planData](t, moved); m.PlanHash != d.PlanHash || m.Sources["skipped:unmerged-branch"] != 2 {
		t.Fatalf("a new local branch is listed but must not change the plan hash: %+v", m)
	}
}

// The same commit must give the same plan hash in another clone, whatever local branches and
// worktrees that clone has, and on any day: the churn window is anchored on HEAD's commit time.
func TestBootstrapPlanHashIsTheSameInAnotherCloneAndOnAnotherDay(t *testing.T) {
	dir, c := bootstrapRepo(t)
	first, code, _ := runBootstrapJSON(t, c, dir, "plan")
	if code != ExitOK {
		t.Fatalf("plan: %+v", first)
	}
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	clone := filepath.Join(parent, "clone")
	gitIn(t, parent, "clone", "-q", dir, clone)
	gitIn(t, clone, "branch", "local-only")
	gitIn(t, clone, "checkout", "-q", "-b", "wip")
	if err := os.WriteFile(filepath.Join(clone, "wip.go"), []byte("package wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, clone, "add", "-A")
	gitIn(t, clone, "commit", "-qm", "local work")
	gitIn(t, clone, "checkout", "-q", "main")
	c2 := newCLI(t)
	if _, e, code := c2.run(clone, "init"); code != ExitOK {
		t.Fatalf("init in the clone: %s", e)
	}
	// project-memory/ came with the clone, so init leaves it untrusted; there is no trust command
	// yet, so the test records the decision a person would make.
	cp, err := openProject(context.Background(), &Env{Getenv: c2.getenv(), Cwd: clone}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := cp.Store.RecordTrust(context.Background(), cp.Repo.CommonDir, store.TrustTrusted, "test"); err != nil {
		t.Fatal(err)
	}
	cp.Close()
	second, code, _ := runBootstrapJSON(t, c2, clone, "plan")
	d1, d2 := decodeData[planData](t, first), decodeData[planData](t, second)
	if code != ExitOK || d2.Sources["skipped:unmerged-branch"] != 1 || d1.PlanHash != d2.PlanHash {
		t.Fatalf("another clone of the same commit with other local branches must plan the same hash: code=%d %+v\n%+v\n%+v", code, second.Error, d1, d2)
	}
}

// The churn window ends at HEAD's committer time: a repository whose history is years old still
// has churn, and the window does not move with the clock.
func TestChurnWindowIsAnchoredOnHeadNotTheClock(t *testing.T) {
	dir := newGitRepo(t)
	old := "2001-02-03T04:05:06Z"
	c := exec.Command("git", "-C", dir, "commit", "-q", "--allow-empty", "-m", "old")
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid",
		"GIT_COMMITTER_DATE="+old, "GIT_AUTHOR_DATE="+old)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	repo, err := gitx.Discover(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := churnSince(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC).Add(-90 * 24 * time.Hour)
	if !got.Equal(want) {
		t.Fatalf("churn window starts at %s, want %s (90 days before HEAD's commit time)", got, want)
	}
}

func TestBootstrapPlanCountsWhatASourceDirectoryDropped(t *testing.T) {
	dir, c := bootstrapRepo(t)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(outside, "notes.md"), []byte("internal/queue was rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "README.md"), filepath.Join(outside, "link.md")); err != nil {
		t.Skip("symlinks are not available here: " + err.Error())
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "plan", "--source", "notes="+outside)
	d := decodeData[planData](t, e)
	if code != ExitOK || d.Sources["skipped:link"] != 1 || d.Sources["queued"] < 4 {
		t.Fatalf("a link in a --source directory is counted, not lost: code=%d %+v", code, d.Sources)
	}
}

func TestBootstrapPlanSourceFlag(t *testing.T) {
	dir, c := bootstrapRepo(t)
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.WriteFile(filepath.Join(outside, "notes.md"), []byte("internal/queue was rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, code, _ := runBootstrapJSON(t, c, dir, "plan", "--source", "notes="+outside)
	d := decodeData[planData](t, e)
	if code != ExitOK || !strings.Contains(d.Scope, "notes") {
		t.Fatalf("code=%d %+v", code, d)
	}
	for _, bad := range []string{"Bad=" + outside, "repo=" + outside, "git=" + outside, "noequals", "n=" + filepath.Join(outside, "missing")} {
		if _, code, _ := runBootstrapJSON(t, c, dir, "plan", "--source", bad); code == ExitOK {
			t.Errorf("--source %q should be refused", bad)
		}
	}
}

func TestBootstrapNeedsATrustedRegisteredCheckout(t *testing.T) {
	dir := newGitRepo(t)
	c := newCLI(t)
	e, code, _ := runBootstrapJSON(t, c, dir, "plan")
	if code == ExitOK || e.OK {
		t.Fatalf("an uninitialised repository must refuse: %+v", e)
	}
}

func TestBootstrapUsage(t *testing.T) {
	dir, c := bootstrapRepo(t)
	for _, args := range [][]string{{}, {"nope"}, {"plan", "--sample", "0"}, {"plan", "--bogus"}, {"plan", "extra"}} {
		if _, code, _ := runBootstrapJSON(t, c, dir, args...); code != ExitUsage {
			t.Errorf("bootstrap %v: code %d, want %d", args, code, ExitUsage)
		}
	}
}

// The scope is part of the plan hash, so the order the --source flags were typed in must not be.
func TestBootstrapPlanScopeAndHashDoNotDependOnSourceFlagOrder(t *testing.T) {
	dir, c := bootstrapRepo(t)
	mk := func(name, text string) string {
		d, _ := filepath.EvalSymlinks(t.TempDir())
		if err := os.WriteFile(filepath.Join(d, name+".md"), []byte(text+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return d
	}
	a, b := mk("a", "internal/queue notes"), mk("b", "internal/queue ADRs")
	first, code, _ := runBootstrapJSON(t, c, dir, "plan", "--source", "alpha="+a, "--source", "beta="+b)
	second, code2, _ := runBootstrapJSON(t, c, dir, "plan", "--source", "beta="+b, "--source", "alpha="+a)
	d1, d2 := decodeData[planData](t, first), decodeData[planData](t, second)
	if code != ExitOK || code2 != ExitOK || d1.Scope != d2.Scope || d1.PlanHash != d2.PlanHash {
		t.Fatalf("the same sources in another flag order are the same plan: %+v %+v", d1, d2)
	}
}

func TestBootstrapPlanRefusesTheSameSourceNameTwice(t *testing.T) {
	dir, c := bootstrapRepo(t)
	one, _ := filepath.EvalSymlinks(t.TempDir())
	two, _ := filepath.EvalSymlinks(t.TempDir())
	e, code, _ := runBootstrapJSON(t, c, dir, "plan", "--source", "notes="+one, "--source", "notes="+two)
	if code != ExitUsage || e.OK || !strings.Contains(e.Error.Message, "twice") {
		t.Fatalf("two directories under one name would collide in the run: code=%d %+v", code, e)
	}
}

// forceState sets a run's state directly. Confirm (Task 14) is the real way; earlier tasks'
// tests use this to reach the states they need.
func forceState(t *testing.T, c *cli, runID, state string) {
	t.Helper()
	var dbPath string
	_ = filepath.WalkDir(c.dataDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "wikory.db" {
			dbPath = p
		}
		return nil
	})
	if dbPath == "" {
		t.Fatal("no wikory.db under the data directory")
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE bs_runs SET state=? WHERE run_id=?`, state, runID); err != nil {
		t.Fatal(err)
	}
}

// forceItemsClosed marks every open item of a run done, so finish can be exercised without
// replaying every submission.
func forceItemsClosed(t *testing.T, c *cli, runID string) {
	t.Helper()
	var dbPath string
	_ = filepath.WalkDir(c.dataDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "wikory.db" {
			dbPath = p
		}
		return nil
	})
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE bs_items SET state='done' WHERE run_id=? AND state IN ('pending','leased')`, runID); err != nil {
		t.Fatal(err)
	}
}
