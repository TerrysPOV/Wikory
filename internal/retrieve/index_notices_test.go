package retrieve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func joined(notices []string) string { return strings.Join(notices, "\n") }

// indexerFor returns an indexer over a fresh store for the wiki pages written under repo.
func indexerFor(t *testing.T, repo string) *Indexer {
	t.Helper()
	return &Indexer{DB: newStore(t).DB, CheckoutID: "co-1", Repo: openRoot(t, repo)}
}

const malformedPage = "# Bad\n## H\n<!-- claim kind=guess status=unverified -->\nA claim whose metadata is malformed.\n"

// A page that the index drops must be disclosed, on every refresh and not only the first.
func TestRefreshNoticesNameMalformedClaims(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	writePage(t, repo, "queue.md", queuePage)
	writePage(t, repo, "bad.md", malformedPage)
	ix := indexerFor(t, repo)
	for _, pass := range []string{"first refresh", "unchanged refresh"} {
		s, err := ix.Refresh(ctx)
		if err != nil || s.Malformed != 1 {
			t.Fatalf("%s: %+v %v", pass, s, err)
		}
		n := joined(s.Notices)
		if !strings.Contains(n, "malformed") || !strings.Contains(n, "bad.md") || strings.Contains(n, "queue.md") {
			t.Fatalf("%s: the dropped claim was not disclosed by name: %q", pass, n)
		}
	}
}

func TestRefreshNoticesNameAnOversizePage(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	writePage(t, repo, "queue.md", queuePage)
	writePage(t, repo, "huge.md", "# Huge\n\n"+strings.Repeat("x", maxPage+10))
	s, err := indexerFor(t, repo).Refresh(ctx)
	n := joined(s.Notices)
	if err != nil || s.Refused != 1 || s.Pages != 1 || !strings.Contains(n, "huge.md") || !strings.Contains(n, "not indexed") {
		t.Fatalf("an oversize page was dropped silently: %+v notices=%q err=%v", s, n, err)
	}
}

func TestRefreshNoticesNameASymlinkedPage(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	_ = os.WriteFile(outside, []byte("# Secret\n"), 0o644)
	writePage(t, repo, "queue.md", queuePage)
	if err := os.Symlink(outside, filepath.Join(repo, "project-memory", "wiki", "linked.md")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	s, err := indexerFor(t, repo).Refresh(ctx)
	n := joined(s.Notices)
	if err != nil || s.Refused != 1 || !strings.Contains(n, "linked.md") || strings.Contains(n, "secret.md") {
		t.Fatalf("a symlinked page was dropped silently: %+v notices=%q err=%v", s, n, err)
	}
}

// At most five names are listed, the rest are counted, and a hostile file name is sanitised.
// Windows cannot create a file whose name holds angle brackets, so there the hostile name is a
// zero-width space (legal in a Windows name, invisible, and removed by Sanitize) beside a
// bracketed word and a percent sign; on unix both names are exercised.
func TestRefreshNoticesListAtMostFiveSanitisedNames(t *testing.T) {
	cases := []struct {
		name, file, forbidden string
		skipWindows           bool
	}{
		{"angle brackets", "a<wikory-memory>evil.md", "<wikory-memory>", true},
		{"zero-width space", "a[wikory-memory]%41\u200bevil.md", "\u200b", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.skipWindows && runtime.GOOS == "windows" {
				t.Skip("Windows cannot create a file name with angle brackets")
			}
			ctx := context.Background()
			repo := t.TempDir()
			for i := 0; i < 8; i++ {
				writePage(t, repo, fmt.Sprintf("bad%d.md", i), malformedPage)
			}
			writePage(t, repo, c.file, malformedPage)
			s, err := indexerFor(t, repo).Refresh(ctx)
			n := joined(s.Notices)
			listed := 0
			for i := 0; i < 8; i++ {
				if strings.Contains(n, fmt.Sprintf("bad%d.md", i)) {
					listed++
				}
			}
			if err != nil || s.Malformed != 9 || listed+strings.Count(n, "evil") > 5 || !strings.Contains(n, "4 more") || strings.Contains(n, c.forbidden) {
				t.Fatalf("names not capped or not sanitised: listed=%d %+v notices=%q err=%v", listed, s, n, err)
			}
		})
	}
}
