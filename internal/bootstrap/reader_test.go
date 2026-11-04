package bootstrap

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/gitx"
)

func TestRepoReaderListsTrackedFilesAtHeadAndReadsByOID(t *testing.T) {
	ctx := context.Background()
	dir, repo := newRepoWith(t, map[string]string{"a/b.go": "package b\n", "README.md": "# hi\n"})
	if err := os.WriteFile(filepath.Join(dir, "untracked.go"), []byte("package u\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rd := NewRepoReader(repo, "HEAD")
	es, err := rd.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range es {
		paths = append(paths, e.Path)
		if e.Source != "repo" || e.OID == "" {
			t.Fatalf("entry %+v", e)
		}
	}
	if !slices.Equal(paths, []string{"README.md", "a/b.go"}) {
		t.Fatalf("paths (sorted, tracked only) = %v", paths)
	}
	b, err := rd.Read(ctx, es[1], 100)
	if err != nil || string(b) != "package b\n" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := rd.Read(ctx, es[1], 3); !errors.Is(err, gitx.ErrTooLarge) {
		t.Fatalf("limit not applied: %v", err)
	}
}

func TestDirReaderWalksThroughTheConfinedRootAndNeverFollowsLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	ctx := context.Background()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "trackers"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	must(os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core]\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "trackers", "a.md"), []byte("note\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "local-wiki.json"), []byte("{}"), 0o644))
	must(os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside\n"), 0o644))
	must(os.Symlink(outside, filepath.Join(dir, "escape")))
	must(os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "link.txt")))
	root, err := confine.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rd := NewDirReader("nimbalyst", root)
	es, err := rd.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, e := range es {
		paths = append(paths, e.Path)
	}
	if !slices.Equal(paths, []string{"local-wiki.json", "trackers/a.md"}) {
		t.Fatalf("a symlinked file or directory, or anything under .git, must not be listed: %v", paths)
	}
	if got := rd.Tier("trackers/a.md"); got != TierAttributed {
		t.Fatalf("an outside file is attributed, got %q", got)
	}
	if got := rd.Tier("decisions/0001.md"); got != TierIntent {
		t.Fatalf("a decision record is intent even outside the repo, got %q", got)
	}
	b, err := rd.Read(ctx, es[1], 100)
	if err != nil || string(b) != "note\n" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestDirReaderRecordsWhatItDrops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks and hard links need privileges on Windows")
	}
	ctx := context.Background()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(dir, "a", "b", "c"), 0o755))
	must(os.MkdirAll(filepath.Join(dir, ".git"), 0o755))
	must(os.WriteFile(filepath.Join(dir, "a", "one.md"), []byte("one\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "a", "b", "c", "deep.md"), []byte("deep\n"), 0o644))
	must(os.WriteFile(filepath.Join(dir, "orig.md"), []byte("x\n"), 0o644))
	must(os.Link(filepath.Join(dir, "orig.md"), filepath.Join(dir, "twin.md")))
	must(os.WriteFile(filepath.Join(outside, "t.txt"), []byte("t\n"), 0o644))
	must(os.Symlink(filepath.Join(outside, "t.txt"), filepath.Join(dir, "link.txt")))
	root, err := confine.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	rd := NewDirReader("notes", root)
	rd.MaxDepth = 3
	es, err := rd.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 1 || es[0].Path != "a/one.md" || es[0].Modified.IsZero() {
		t.Fatalf("only a/one.md is readable, and it carries a modification time: %+v", es)
	}
	got := map[string]string{}
	for _, s := range rd.Skipped() {
		got[s.Path] = s.Reason
		if strings.HasPrefix(s.Path, ".git") {
			t.Errorf(".git is dropped deliberately and is not recorded: %+v", s)
		}
	}
	want := map[string]string{"a/b/c/": DispTooDeep, "link.txt": DispLink, "orig.md": DispUnreadable, "twin.md": DispUnreadable}
	if !maps.Equal(got, want) {
		t.Fatalf("skipped = %v, want %v (a directory not entered, a link, and two files with extra hard links)", got, want)
	}
	if _, err := rd.List(ctx); err != nil || len(rd.Skipped()) != len(want) {
		t.Fatalf("a second List starts afresh instead of appending: %d records, %v", len(rd.Skipped()), err)
	}
}

func TestDirReaderBoundsDepthAndEntryCount(t *testing.T) {
	ctx := context.Background()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	deep := filepath.Join(dir, "a", "b", "c", "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"top.md", "a/one.md", "a/b/c/d/deep.md"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(f)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, _ := confine.Open(dir)
	defer root.Close()
	rd := NewDirReader("x", root)
	rd.MaxDepth = 2
	es, err := rd.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		if e.Path == "a/b/c/d/deep.md" {
			t.Fatal("a file deeper than MaxDepth must not be listed")
		}
	}
	rd.MaxDepth, rd.MaxEntries = 8, 1
	if _, err := rd.List(ctx); err == nil {
		t.Fatal("exceeding MaxEntries must be an error, not a silent truncation")
	}
}
