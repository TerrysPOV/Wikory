package app

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hostileGitFile builds a directory whose .git FILE points at victim's git directory and
// which carries a copy of the victim's committed project-memory. git reports the hostile
// directory as the worktree and the victim's directory as the common dir, so a trust check
// on the common dir alone would trust it.
func hostileGitFile(t *testing.T, victim string, relative bool) string {
	t.Helper()
	hostile, _ := filepath.EvalSymlinks(t.TempDir())
	target := filepath.Join(victim, ".git")
	if relative {
		rel, err := filepath.Rel(hostile, target)
		if err != nil {
			t.Skipf("no relative path from %s to %s: %v", hostile, target, err)
		}
		target = rel
	}
	if err := os.WriteFile(filepath.Join(hostile, ".git"), []byte(fmt.Sprintf("gitdir: %s\n", target)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"config.toml", "wiki/retries.md", "wiki/README.md"} {
		dst := filepath.Join(hostile, "project-memory", filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(readFile(t, filepath.Join(victim, "project-memory", filepath.FromSlash(f)))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return hostile
}

func TestHostileGitFileInheritsNoTrust(t *testing.T) {
	for _, relative := range []bool{false, true} {
		name := map[bool]string{false: "absolute", true: "relative"}[relative]
		t.Run(name, func(t *testing.T) {
			h := newHookRig(t)
			hostile := hostileGitFile(t, h.dir, relative)
			got := h.context(t, hostile)
			if !strings.Contains(got, "not registered") || strings.Contains(got, "Retries are capped") {
				t.Fatalf("a directory that only points at a trusted clone's git dir was served its memory: %s", got)
			}
			out, errOut, code := h.c.run(hostile, "search", "poison")
			if code == ExitOK || strings.Contains(out, "poison message") {
				t.Fatalf("search served a hostile worktree: code=%d out=%s err=%s", code, out, errOut)
			}
			before := h.turnCount(t)
			h.hook(t, hostile, "stop", h.payload("Stop", hostile, map[string]any{"prompt_id": "p9", "last_assistant_message": "x"}))
			if h.turnCount(t) != before {
				t.Fatal("a hostile worktree was captured")
			}
		})
	}
}

// wikory init in a directory that only points at a trusted clone's git dir must not register
// it, whatever trust the clone holds.
func TestInitRefusesAHostileGitFile(t *testing.T) {
	h := newHookRig(t)
	hostile := hostileGitFile(t, h.dir, false)
	_, errOut, code := h.c.run(hostile, "init")
	if code == ExitOK || !strings.Contains(errOut, "does not list this directory as a worktree") {
		t.Fatalf("init in a hostile directory: code=%d err=%s", code, errOut)
	}
	if got := h.context(t, hostile); !strings.Contains(got, "not registered") {
		t.Fatalf("a hostile directory became registered through init: %s", got)
	}
}

// core.worktree in .git/config redirects git's idea of the work tree. init must not write
// into the directory it names, nor record it as a trusted checkout.
func TestInitRefusesACoreWorktreeRedirect(t *testing.T) {
	dir := newGitRepo(t)
	sibling, _ := filepath.EvalSymlinks(t.TempDir())
	gitIn(t, dir, "config", "core.worktree", sibling)
	c := newCLI(t)
	_, errOut, code := c.run(dir, "init")
	if code == ExitOK || !strings.Contains(errOut, "core.worktree") {
		t.Fatalf("init with core.worktree: code=%d err=%s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(sibling, "project-memory")); err == nil {
		t.Fatal("init wrote into the redirected work tree")
	}
	if _, err := os.Stat(filepath.Join(dir, "project-memory")); err == nil {
		t.Fatal("init wrote project-memory despite refusing")
	}
}

// replaceClone deletes dir's .git and makes a different repository there: same path, same
// origin, same committed project-memory, a new common dir identity.
func replaceClone(t *testing.T, dir string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "-c", "init.defaultBranch=main", "init", "-q")
	gitIn(t, dir, "remote", "add", "origin", "git@github.com:demo/repo.git")
}

// Trust is keyed on the clone's common-dir path, so a trusted clone that is deleted and
// replaced by another repository at the same path must not inherit that trust.
func TestReplacedCloneAtTheSamePathInheritsNoTrust(t *testing.T) {
	h := newHookRig(t)
	if got := h.context(t, h.dir); !strings.HasPrefix(got, "wikory: mode=full") {
		t.Fatalf("the original clone is not served before the swap: %s", got)
	}
	replaceClone(t, h.dir)
	got := h.context(t, h.dir)
	if !strings.Contains(got, "not registered") || strings.Contains(got, "Retries are capped") {
		t.Fatalf("a different repository at a trusted clone's path was served its memory: %s", got)
	}
	// Running init there is the owner's explicit act, but it must not carry over the
	// earlier clone's trust decision: project-memory already existed, so it is undecided.
	if _, errOut, code := h.c.run(h.dir, "init"); code != ExitOK {
		t.Fatalf("init in the replacement: code=%d err=%s", code, errOut)
	}
	if got := h.context(t, h.dir); strings.Contains(got, "Retries are capped") {
		t.Fatalf("init handed the replaced clone's trust to the new repository: %s", got)
	}
}

func TestCloneIdFileIsOwnerOnlyAndStableAcrossInit(t *testing.T) {
	h := newHookRig(t)
	p := filepath.Join(h.dir, ".git", "wikory-checkout-id")
	first := readFile(t, p)
	if first == "" {
		t.Fatal("empty clone id")
	}
	if fi, err := os.Stat(p); err != nil || (fi.Mode().Perm()&0o077 != 0 && runtime.GOOS != "windows") {
		t.Fatalf("clone id file mode %v err %v", fi.Mode(), err)
	}
	if _, errOut, code := h.c.run(h.dir, "init"); code != ExitOK {
		t.Fatalf("second init: %s", errOut)
	}
	if second := readFile(t, p); second != first {
		t.Fatalf("a second init rewrote the clone id: %q -> %q", first, second)
	}
}

// storeDB opens the single private store the rig created.
func storeDB(t *testing.T, h *hookRig) *sql.DB {
	t.Helper()
	dbs, _ := filepath.Glob(filepath.Join(h.c.dataDir, "stores", "*", "wikory.db"))
	if len(dbs) != 1 {
		t.Fatalf("want one store, found %v", dbs)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbs[0])+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func count(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// makeLegacy turns the rig's clone into one a pre-nonce build registered: trusted, registered,
// but with no clone id in the store or in the git directory.
func makeLegacy(t *testing.T, h *hookRig) {
	t.Helper()
	db := storeDB(t, h)
	if _, err := db.Exec(`DELETE FROM meta WHERE k LIKE 'clone-id:%'`); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.dir, ".git", "wikory-checkout-id")); err != nil {
		t.Fatal(err)
	}
}

// A clone the store has trust or registrations for, but no recorded clone id, cannot be
// verified. init must not keep its trusted decision.
func TestInitKeepsNoTrustForAClonePredatingCloneIds(t *testing.T) {
	h := newHookRig(t)
	makeLegacy(t, h)
	if got := h.context(t, h.dir); !strings.Contains(got, "not registered") {
		t.Fatalf("an unverifiable clone was served before init: %s", got)
	}
	out, errOut, code := h.c.run(h.dir, "init")
	if code != ExitOK {
		t.Fatalf("init: code=%d %s", code, errOut)
	}
	if got := h.context(t, h.dir); strings.Contains(got, "Retries are capped") {
		t.Fatalf("init kept a trusted decision it could not verify: %s", got)
	}
	if !strings.Contains(out+errOut, "discarded") {
		t.Fatalf("the user was not told the earlier trust was discarded: %s %s", out, errOut)
	}
}

// Revoke first, bind second, then write the file: a failure at any step leaves the path
// untrusted, never trusted.
func TestCloneIdFailureAtAnyStepLeavesThePathUntrusted(t *testing.T) {
	for _, scenario := range []string{"replaced", "predates clone ids"} {
		for _, stage := range []string{"before-revoke", "after-revoke", "after-file"} {
			t.Run(scenario+"/"+stage, func(t *testing.T) {
				h := newHookRig(t)
				if scenario == "replaced" {
					replaceClone(t, h.dir)
				} else {
					makeLegacy(t, h)
				}
				testHookCloneID = func(s string) error {
					if s == stage {
						return fmt.Errorf("injected failure at %s", s)
					}
					return nil
				}
				t.Cleanup(func() { testHookCloneID = nil })
				if _, errOut, code := h.c.run(h.dir, "init"); code == ExitOK || !strings.Contains(errOut, "injected failure") {
					t.Fatalf("the injected failure did not stop init: code=%d err=%s", code, errOut)
				}
				testHookCloneID = nil
				if got := h.context(t, h.dir); strings.Contains(got, "Retries are capped") {
					t.Fatalf("a failure at %s left the path trusted: %s", stage, got)
				}
			})
		}
	}
}

// A replacement clone gets new checkout ids and finds none of the old clone's private rows.
func TestReplacedCloneSeesNoPrivateDataOfTheOldOne(t *testing.T) {
	h := newHookRig(t)
	h.hook(t, h.dir, "user-prompt-submit", h.payload("UserPromptSubmit", h.dir, map[string]any{"prompt": "OLD-CLONE-PROMPT", "prompt_id": "p1"}))
	h.hook(t, h.dir, "stop", h.payload("Stop", h.dir, map[string]any{"prompt_id": "p1", "last_assistant_message": "done"}))
	h.context(t, h.dir) // builds the page index
	db := storeDB(t, h)
	oldID := ""
	if err := db.QueryRow(`SELECT id FROM checkouts`).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`SELECT count(*) FROM turns`, `SELECT count(*) FROM jobs`, `SELECT count(*) FROM job_ops`, `SELECT count(*) FROM pages`, `SELECT count(*) FROM claims`} {
		if count(t, db, q) == 0 {
			t.Fatalf("the test seeded nothing for %q", q)
		}
	}
	replaceClone(t, h.dir)
	d, _ := initJSON(t, h.c, h.dir)
	if d.Checkout == oldID {
		t.Fatalf("the replacement clone got the old checkout id %s", oldID)
	}
	for _, table := range []string{"turn_starts", "turns", "jobs", "pages", "claims", "fts", "checkouts"} {
		col := "checkout_id"
		if table == "checkouts" {
			col = "id"
		}
		if n := count(t, db, `SELECT count(*) FROM `+table+` WHERE `+col+` != ?`, d.Checkout); n != 0 {
			t.Errorf("%d row(s) of the old clone survive in %s", n, table)
		}
	}
	if n := count(t, db, `SELECT count(*) FROM job_ops`); n != 0 {
		t.Errorf("%d job_ops row(s) of the old clone survive", n)
	}
}
