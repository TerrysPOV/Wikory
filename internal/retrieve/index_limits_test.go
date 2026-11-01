package retrieve

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func countPages(t *testing.T, ix *Indexer) int {
	t.Helper()
	var n int
	if err := ix.DB.QueryRow(`SELECT count(*) FROM pages WHERE checkout_id = ?`, ix.CheckoutID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func bigPage(i int) string {
	return fmt.Sprintf("# Page %d\n\n%s\n", i, strings.Repeat("filler words here ", 40))
}

func TestRefreshPageCapMarksIndexPartial(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	for i := 0; i < 8; i++ { // 8 pages against a cap of 3: exceeds the bound
		writePage(t, repo, fmt.Sprintf("p%d.md", i), bigPage(i))
	}
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo), MaxPages: 3}
	s, err := ix.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Pages != 3 || s.Skipped < 1 || !s.Partial || len(s.Notices) == 0 {
		t.Fatalf("page cap not enforced or not reported: %+v", s)
	}
	if n := countPages(t, ix); n != 3 {
		t.Fatalf("%d pages indexed against a cap of 3", n)
	}
}

func TestRefreshByteCapMarksIndexPartial(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	for i := 0; i < 5; i++ { // each page is ~700 bytes, 5 pages is ~3500 against a cap of 1000
		writePage(t, repo, fmt.Sprintf("p%d.md", i), bigPage(i))
	}
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo), MaxTotalBytes: 1000}
	s, err := ix.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Changed != 1 || s.Skipped != 4 || !s.Partial || len(s.Notices) == 0 {
		t.Fatalf("byte cap not enforced or not reported: %+v", s)
	}
	if n := countPages(t, ix); n != 1 {
		t.Fatalf("%d pages indexed past the byte cap", n)
	}
}

func TestRefreshDepthCapMarksIndexPartial(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "a/ok.md", "# Ok\n\nShallow.\n")
	writePage(t, repo, "a/b/c/d/deep.md", "# Deep\n\nToo deep.\n") // depth 4 against a cap of 2
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo), MaxDepth: 2}
	s, err := ix.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Pages != 1 || s.Skipped < 1 || !s.Partial || len(s.Notices) == 0 {
		t.Fatalf("depth cap not enforced or not reported: %+v", s)
	}
	if ftsCount(t, st, "co-1", "project-memory/wiki/a/b/c/d/deep.md") != 0 {
		t.Fatal("a page beyond the depth cap was indexed")
	}
}

// A walk cut short by a limit must not read as "these pages were deleted".
func TestPartialRefreshDoesNotRemoveUnseenPages(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	for i := 0; i < 6; i++ {
		writePage(t, repo, fmt.Sprintf("p%d.md", i), bigPage(i))
	}
	ix := &Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}
	if _, err := ix.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	ix.MaxPages = 2
	s, err := ix.Refresh(context.Background())
	if err != nil || !s.Partial {
		t.Fatalf("%+v %v", s, err)
	}
	if s.Removed != 0 || countPages(t, ix) != 6 {
		t.Fatalf("a partial walk deleted indexed pages: %+v, %d rows", s, countPages(t, ix))
	}
}

func TestRefreshCompleteIsNotPartial(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "queue.md", queuePage)
	s, err := (&Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}).Refresh(context.Background())
	if err != nil || s.Partial || s.Skipped != 0 || len(s.Notices) != 0 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestRefreshReturnsContextCancellation(t *testing.T) {
	st := newStore(t)
	repo := t.TempDir()
	writePage(t, repo, "queue.md", queuePage)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}).Refresh(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled refresh did not report cancellation: %v", err)
	}
}

// project-memory (or the wiki dir itself) being a regular file is a refusal, not a failure.
func TestRefreshWikiPathThroughRegularFileIsRefusal(t *testing.T) {
	for _, tc := range []struct{ name, file string }{
		{"wiki is a file", "project-memory/wiki"},
		{"project-memory is a file", "project-memory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newStore(t)
			repo := t.TempDir()
			writeRaw(t, repo, tc.file, "not a directory")
			s, err := (&Indexer{DB: st.DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}).Refresh(context.Background())
			if err != nil || s.Refused != 1 || s.Pages != 0 {
				t.Fatalf("%+v %v", s, err)
			}
		})
	}
}
