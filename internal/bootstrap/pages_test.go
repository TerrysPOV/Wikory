package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/publish"
)

func TestPlanOpsCreatesUnchangedAndConflictRules(t *testing.T) {
	pages := []PageOut{
		{Path: "p/new.md", Content: []byte("new")},
		{Path: "p/same.md", Content: []byte("same")},
		{Path: "p/mine.md", Content: []byte("regenerated")},
		{Path: "p/edited.md", Content: []byte("regenerated")},
		{Path: "p/stranger.md", Content: []byte("regenerated")},
	}
	files := map[string]string{"p/same.md": "same", "p/mine.md": "old bootstrap text", "p/edited.md": "a human changed this", "p/stranger.md": "someone else's page"}
	rec := map[string]string{"p/mine.md": publish.HashContent([]byte("old bootstrap text")), "p/edited.md": publish.HashContent([]byte("old bootstrap text"))}
	ops, conflicts, unchanged, err := PlanOps(pages,
		func(p string) ([]byte, bool, error) { s, ok := files[p]; return []byte(s), ok, nil },
		func(p, h string) bool { r, ok := rec[p]; return ok && r == h })
	if err != nil {
		t.Fatal(err)
	}
	byTarget := map[string]publish.Op{}
	for _, o := range ops {
		byTarget[o.Target] = o
	}
	if o, ok := byTarget["p/new.md"]; !ok || o.OldHash != publish.Absent {
		t.Fatalf("a new page must be created only if still absent: %+v", o)
	}
	if o, ok := byTarget["p/mine.md"]; !ok || o.OldHash != publish.HashContent([]byte("old bootstrap text")) {
		t.Fatalf("our own earlier output is replaced against its exact content: %+v", o)
	}
	if len(ops) != 2 {
		t.Fatalf("only the new page and our own page are written: %+v", ops)
	}
	if len(unchanged) != 1 || unchanged[0] != "p/same.md" {
		t.Fatalf("%v", unchanged)
	}
	if len(conflicts) != 2 {
		t.Fatalf("a human-edited page and a page we never wrote are conflicts, never overwritten: %v", conflicts)
	}
}

func TestPlanOpsPropagatesAReadError(t *testing.T) {
	boom := errors.New("boom")
	_, _, _, err := PlanOps([]PageOut{{Path: "p/a.md", Content: []byte("x")}},
		func(string) ([]byte, bool, error) { return nil, false, boom }, func(string, string) bool { return false })
	if !errors.Is(err, boom) {
		t.Fatalf("%v", err)
	}
}

func TestScanPagesNamesThePageAndKindNeverTheText(t *testing.T) {
	secret := "hunter2hunter2"
	pages := []PageOut{
		{Path: "p/ok.md", Content: []byte("The queue acknowledges after the handler returns.\n")},
		{Path: "p/bad.md", Content: []byte("The deploy script sets pass" + "word=" + secret + " before the queue starts.\n")},
	}
	got := ScanPages(pages)
	if len(got) != 1 || got[0].Path != "p/bad.md" || len(got[0].Kinds) == 0 {
		t.Fatalf("only the page with a secret-shaped value is named, with the kind of each span: %+v", got)
	}
	if strings.Contains(fmt.Sprintf("%+v", got), secret) {
		t.Fatal("the scan result must never hold the matched text")
	}
}

func TestPublishedHashesAreRecordedPerCheckout(t *testing.T) {
	ctx := context.Background()
	r, _, _, _ := newTestRuns(t)
	run := mustRun(t, r, 0, 1)
	if _, ok, _ := r.PublishedHash(ctx, "p/a.md"); ok {
		t.Fatal("nothing recorded yet")
	}
	if err := r.RecordPublished(ctx, run.ID, map[string]string{"p/a.md": "h1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPublished(ctx, run.ID, map[string]string{"p/a.md": "h2"}); err != nil {
		t.Fatal(err)
	}
	if h, ok, _ := r.PublishedHash(ctx, "p/a.md"); !ok || h != "h2" {
		t.Fatalf("a later publish replaces the recorded hash: %q %v", h, ok)
	}
	other := *r
	other.CheckoutID = "elsewhere"
	if _, ok, _ := other.PublishedHash(ctx, "p/a.md"); ok {
		t.Fatal("recorded hashes belong to one checkout")
	}
}

// A page is more than its claims: a component name reaches the title, the links and the listing.
// ScanPages is the last gate before publishing, so it has to see text that no claim holds.
func TestScanPagesSeesSecretShapedTextOutsideClaims(t *testing.T) {
	prefix := "internal/pass" + "word=hunter2hunter2"
	m := RenderModel{
		Run:        Run{ID: "run_1", Head: "c0ffee0123456789c0ffee0123456789c0ffee01"},
		Date:       "2026-11-03",
		Components: []Component{{Name: prefix, Prefix: prefix, Rank: 1, Inspected: true, Files: 1}},
	}
	got := ScanPages(RenderPages(m))
	if len(got) == 0 {
		t.Fatal("a secret-shaped component name must be reported before any page is published")
	}
	for _, g := range got {
		// The page path is a slug of the component name, so it can echo the name itself: the
		// guarantee is about the kinds, and inventory keeps secret-shaped names out of components.
		if len(g.Kinds) == 0 || strings.Contains(fmt.Sprintf("%v", g.Kinds), "hunter2") {
			t.Fatalf("a finding names the page and the kinds, never the matched text: %+v", g)
		}
	}
}
