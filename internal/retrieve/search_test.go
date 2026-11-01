package retrieve

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/gitx"
	"github.com/TerrysPOV/Wikory/internal/store"
	"github.com/TerrysPOV/Wikory/internal/validate"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// stores gives each test one store, so several searches in one test share one database.
var stores = map[string]*store.Store{}

func sharedStore(t *testing.T) *store.Store {
	t.Helper()
	if st, ok := stores[t.Name()]; ok {
		return st
	}
	st := newStore(t)
	stores[t.Name()] = st
	return st
}

func searchIn(t *testing.T, dir, checkout, q string, opts SearchOpts) []Hit {
	t.Helper()
	ctx := context.Background()
	st := sharedStore(t)
	root := openRoot(t, dir)
	if _, err := (&Indexer{DB: st.DB, CheckoutID: checkout, Repo: root}).Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	repo, err := gitx.Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := Search(ctx, st.DB, checkout, root, validate.GitEnv{Repo: repo, Root: root, MaxBytes: 1 << 20}, q, opts)
	if err != nil {
		t.Fatal(err)
	}
	return hits
}

func newRepoWithCode(t *testing.T) string {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	gitT(t, dir, "-c", "init.defaultBranch=main", "init", "-q")
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src", "queue.go"), []byte("package src\n"), 0o644)
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "code")
	return dir
}

func TestSearchMatchesIdentifierForms(t *testing.T) {
	dir := newRepoWithCode(t)
	writePage(t, dir, "queue.md", queuePage)
	for _, q := range []string{"enqueue_job", "enqueue job", "enqueueJob"} {
		hits := searchIn(t, dir, "co-1", q, SearchOpts{Limit: 5})
		if len(hits) == 0 || hits[0].RelPath != "project-memory/wiki/queue.md" || hits[0].PageOnly {
			t.Fatalf("%q: %+v", q, hits)
		}
	}
}

func TestSearchScopeIsThisCheckoutOnly(t *testing.T) {
	a := newRepoWithCode(t)
	b := newRepoWithCode(t)
	// In A the zebra claim is the best possible match. B has a page at the same path with
	// unrelated text, so a missing scope predicate cannot be hidden by the presence check.
	writePage(t, a, "zebra.md", "---\ntitle: Zebra zebra\n---\n<!-- claim kind=intent status=unverified -->\nzebra zebra zebra crossing.\n")
	writePage(t, b, "zebra.md", "# Notes\n\nNothing here.\n")
	writePage(t, b, "horse.md", "---\ntitle: Horse\n---\n<!-- claim kind=intent status=unverified -->\nA horse, not a zebra.\n")
	_ = searchIn(t, a, "co-A", "zebra", SearchOpts{Limit: 5})
	hits := searchIn(t, b, "co-B", "zebra", SearchOpts{Limit: 5})
	if len(hits) == 0 {
		t.Fatal("B's own page should match")
	}
	for _, h := range hits {
		if strings.Contains(h.Text, "crossing") {
			t.Fatalf("checkout A's claim was served in B: %+v", h)
		}
	}
}

func TestSearchAfterBranchSwitch(t *testing.T) {
	dir := newRepoWithCode(t)
	gitT(t, dir, "checkout", "-qb", "feature")
	writePage(t, dir, "feature.md", "# Feature\n\n<!-- claim kind=intent status=unverified -->\nPlatypus migration plan.\n")
	gitT(t, dir, "add", ".")
	gitT(t, dir, "commit", "-qm", "page")
	if len(searchIn(t, dir, "co-1", "platypus", SearchOpts{Limit: 5})) == 0 {
		t.Fatal("the feature page should be found on its branch")
	}
	gitT(t, dir, "checkout", "-q", "main")
	if hits := searchIn(t, dir, "co-1", "platypus", SearchOpts{Limit: 5}); len(hits) != 0 {
		t.Fatalf("a page absent from this tree was served after a branch switch: %+v", hits)
	}
}

func TestSupersededExcludedUnlessHistory(t *testing.T) {
	dir := newRepoWithCode(t)
	writePage(t, dir, "old.md", "# Old\n\n<!-- claim kind=rationale status=superseded -->\nWombat retries were once unlimited.\n")
	for _, h := range searchIn(t, dir, "co-1", "wombat", SearchOpts{Limit: 5}) {
		if !h.PageOnly {
			t.Fatalf("superseded claim served without --history: %+v", h)
		}
	}
	found := false
	for _, h := range searchIn(t, dir, "co-1", "wombat", SearchOpts{Limit: 5, History: true}) {
		found = found || (!h.PageOnly && h.CommittedStatus == "superseded")
	}
	if !found {
		t.Fatal("--history should include the superseded claim")
	}
}

func TestSearchDerivesStatusAndFreshness(t *testing.T) {
	dir := newRepoWithCode(t)
	oid := gitT(t, dir, "rev-parse", "HEAD:src/queue.go")
	text := "Queue workers drain FIFO."
	meta := "<!-- claim c=01J8Q4ZK8N3X2T5V6W7Y8Z9A0B kind=current-impl status=verified vrev=" + gitT(t, dir, "rev-parse", "--short=12", "HEAD") +
		" scope=repo ev=src/queue.go@sha1:" + oid + " dep=src/queue.go th=" + claim.TextHash(text) + " -->"
	writePage(t, dir, "q.md", "# Q\n\n"+meta+"\n"+text+"\n")
	hits := searchIn(t, dir, "co-1", "fifo", SearchOpts{Limit: 5})
	if len(hits) == 0 || hits[0].Display.Status != "verified" || hits[0].Display.Stale {
		t.Fatalf("%+v", hits)
	}
	_ = os.WriteFile(filepath.Join(dir, "src", "queue.go"), []byte("package src\n// changed\n"), 0o644)
	hits = searchIn(t, dir, "co-1", "fifo", SearchOpts{Limit: 5})
	if !hits[0].Display.Stale || !strings.HasPrefix(hits[0].Display.Freshness, "STALE: src/queue.go") {
		t.Fatalf("%+v", hits[0].Display)
	}
}

// A partial index must be declared in the output (the notice comes from RefreshStats).
func TestRenderHitsSaysWhenIndexIsPartial(t *testing.T) {
	dir := newRepoWithCode(t)
	writePage(t, dir, "a.md", "# A\n\n<!-- claim kind=intent status=unverified -->\nQuokka alpha.\n")
	writePage(t, dir, "b.md", "# B\n\n<!-- claim kind=intent status=unverified -->\nQuokka beta.\n")
	ctx := context.Background()
	st := newStore(t)
	root := openRoot(t, dir)
	repo, err := gitx.Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	env := validate.GitEnv{Repo: repo, Root: root, MaxBytes: 1 << 20}
	stats, err := (&Indexer{DB: st.DB, CheckoutID: "co-1", Repo: root, MaxPages: 1}).Refresh(ctx)
	if err != nil || !stats.Partial {
		t.Fatalf("the cut-short refresh should be partial: %+v %v", stats, err)
	}
	hits, err := Search(ctx, st.DB, "co-1", root, env, "quokka", SearchOpts{Limit: 5})
	if err != nil || len(hits) == 0 {
		t.Fatalf("%+v %v", hits, err)
	}
	if out := RenderHits(hits, stats.Notices...); !strings.Contains(out, "partial") {
		t.Fatalf("a partial index was presented as complete:\n%s", out)
	}
	full, err := (&Indexer{DB: st.DB, CheckoutID: "co-1", Repo: root}).Refresh(ctx)
	if err != nil || full.Partial {
		t.Fatalf("%+v %v", full, err)
	}
	if out := RenderHits(hits, full.Notices...); strings.Contains(out, "partial") {
		t.Fatalf("a complete index was reported as partial:\n%s", out)
	}
}
