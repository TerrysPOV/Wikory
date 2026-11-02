package retrieve

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/store"
)

func slack() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("WIKORY_LATENCY_SLACK")); err == nil && n > 0 {
		return time.Duration(n)
	}
	return 1
}

func hasNotice(s RefreshStats, sub string) bool {
	for _, n := range s.Notices {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}

// Review H1: pages are read, hashed and parsed before the database write lock is taken, so a
// slow parse cannot hold up another session's write. The fake clock runs during phase 1 and
// tries to take the lock from outside: it must succeed.
func TestPagesAreParsedOutsideTheWriteLock(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "a.md", queuePage)
	writePage(t, repo, "b.md", "# B\n\nPlain prose.\n")
	var mu sync.Mutex
	var lockErrs []error
	clock := func() time.Time {
		ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond*slack())
		defer cancel()
		err := store.WithWriteLockAt(ctx, st.WriteLockPath(), func(context.Context) error { return nil })
		mu.Lock()
		lockErrs = append(lockErrs, err)
		mu.Unlock()
		return time.Now()
	}
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo), WriteLockPath: st.WriteLockPath(), Now: clock}
	s, err := ix.Refresh(context.Background())
	if err != nil || s.Changed != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if len(lockErrs) == 0 {
		t.Fatal("the parse clock never ran")
	}
	for _, e := range lockErrs {
		if e != nil {
			t.Fatalf("the write lock was held while pages were being parsed: %v", e)
		}
	}
}

// Review H1(b): an unchanged page is not parsed again, and what the index must say about it
// (malformed claims, parts left out) is read from the index.
func TestUnchangedPagesAreNeverReparsedButStillDisclosed(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "bad.md", "# Bad\n\n<!-- claim kind=intent status=unverified dep=a--b -->\nBroken metadata.\n")
	writePage(t, repo, "html.md", "# Html\n\nPara <span hidden>X</span> words.\n")
	writePage(t, repo, "ok.md", queuePage)
	parses := 0
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo), Now: func() time.Time { parses++; return time.Now() }}
	first, err := ix.Refresh(context.Background())
	if err != nil || first.Changed != 3 || first.Malformed != 1 || !hasNotice(first, "malformed") || !hasNotice(first, "indexed only in part") {
		t.Fatalf("first refresh: %+v %v", first, err)
	}
	parses = 0
	second, err := ix.Refresh(context.Background())
	if err != nil || second.Changed != 0 {
		t.Fatalf("second refresh: %+v %v", second, err)
	}
	if parses != 0 {
		t.Fatalf("unchanged pages were parsed again (%d clock reads)", parses)
	}
	if second.Malformed != 1 || !hasNotice(second, "malformed") || !hasNotice(second, "indexed only in part") {
		t.Fatalf("the disclosure was lost for unchanged pages: %+v", second)
	}
}

// Review H1(d): when the parse budget is used up, the remaining changed pages are indexed by
// title only, the index is marked partial, and the next refresh parses them in full.
func TestParseBudgetLeavesLaterPagesTitleOnlyUntilTheNextRefresh(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	for _, n := range []string{"a", "b", "c"} {
		writePage(t, repo, n+".md", "# Page "+n+"\n\nUnique"+n+"word body text.\n")
	}
	var tick time.Time
	slow := func() time.Time { tick = tick.Add(2 * time.Second); return tick }
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo), ParseBudget: time.Second, Now: slow}
	s, err := ix.Refresh(context.Background())
	if err != nil || s.Budgeted != 2 || !s.Partial || !hasNotice(s, "parse time budget") {
		t.Fatalf("budget not applied: %+v %v", s, err)
	}
	bodies := func() int {
		var n int
		if err := st.DB.QueryRow(`SELECT count(*) FROM fts WHERE checkout_id = 'co-1' AND body LIKE '%Unique%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := bodies(); n != 1 {
		t.Fatalf("%d page bodies indexed under an exhausted budget, want 1", n)
	}
	ix.Now, ix.ParseBudget = nil, 0
	s, err = ix.Refresh(context.Background())
	if err != nil || s.Changed != 2 || s.Budgeted != 0 || s.Partial {
		t.Fatalf("retry refresh: %+v %v", s, err)
	}
	if n := bodies(); n != 3 {
		t.Fatalf("%d page bodies after the retry, want 3", n)
	}
}

// Review H1(c): the two review shapes at 240 KB and 255 KB go through Refresh quickly and are
// indexed by title with a disclosure.
func TestPathologicalPagesRefreshQuicklyAndAreDisclosed(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "links.md", "# Links\n\n"+strings.Repeat(strings.Repeat("[a](", 20)+"\n", 3000))
	writePage(t, repo, "emph.md", "# Emph\n\n"+strings.Repeat(strings.Repeat("*a_", 28)+"\n", 3040))
	writePage(t, repo, "oneline.md", "# One\n\n"+strings.Repeat("[a](", 60000)+"\n")
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}
	start := time.Now()
	s, err := ix.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 2*time.Second*slack() {
		t.Errorf("refresh of three pathological pages took %v", el)
	}
	if !hasNotice(s, "indexed only in part") || s.Budgeted != 0 {
		t.Errorf("no disclosure: %+v", s)
	}
	for _, n := range []string{"links", "emph", "oneline"} {
		if ftsCount(t, st, "co-1", "project-memory/wiki/"+n+".md") == 0 {
			t.Errorf("%s was not indexed at all", n)
		}
	}
}

// An index written before page_notes existed has no notes for its pages: they are parsed once.
func TestPagesWithoutNotesAreParsedOnce(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "queue.md", queuePage)
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}
	if _, err := ix.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(fmt.Sprintf(`DELETE FROM page_notes WHERE checkout_id = %q`, "co-1")); err != nil {
		t.Fatal(err)
	}
	s, _ := ix.Refresh(context.Background())
	if s.Changed != 1 {
		t.Fatalf("a page with no notes was not reparsed: %+v", s)
	}
	s, _ = ix.Refresh(context.Background())
	if s.Changed != 0 {
		t.Fatalf("notes were not written: %+v", s)
	}
}
