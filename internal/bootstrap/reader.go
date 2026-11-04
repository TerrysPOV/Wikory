package bootstrap

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/gitx"
)

// TreeSource is the part of gitx.Repo that RepoReader needs.
type TreeSource interface {
	ListTree(ctx context.Context, rev string) ([]gitx.TreeEntry, error)
	ReadBlob(ctx context.Context, oid string, max int64) ([]byte, error)
}

// RepoReader reads the tracked files of one revision. Untracked and ignored files are not
// listed, so a bootstrap baselines what the repository holds, not what is lying about.
type RepoReader struct {
	src TreeSource
	rev string
}

// NewRepoReader reads rev (HEAD or a hex commit id) through src.
func NewRepoReader(src TreeSource, rev string) *RepoReader { return &RepoReader{src: src, rev: rev} }

func (r *RepoReader) Name() string         { return "repo" }
func (r *RepoReader) Tier(p string) string { return TierOf(p) }
func (r *RepoReader) List(ctx context.Context) ([]Entry, error) {
	tree, err := r.src.ListTree(ctx, r.rev)
	if err != nil {
		return nil, err
	}
	es := make([]Entry, 0, len(tree))
	for _, t := range tree {
		es = append(es, Entry{Source: "repo", Path: t.Path, OID: t.OID, Size: t.Size})
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Path < es[j].Path })
	return es, nil
}
func (r *RepoReader) Read(ctx context.Context, e Entry, max int64) ([]byte, error) {
	return r.src.ReadBlob(ctx, e.OID, max)
}

// DirReader reads a directory the person opted in (an existing wiki, a memory folder, planning
// notes). It goes through a confine.Root, so a link inside it is never followed, and it lists
// at most MaxEntries files down to MaxDepth levels. Exceeding MaxEntries is an error: a
// silent truncation would make the coverage report claim more than was read. Everything else
// it does not list is recorded and returned by Skipped. The one thing dropped without a
// record is a .git directory: that is version-control metadata, not the person's content.
type DirReader struct {
	Label      string
	Root       *confine.Root
	MaxDepth   int
	MaxEntries int
	skipped    []Skip
}

// NewDirReader labels the directory (for example "nimbalyst") and bounds the walk.
func NewDirReader(label string, root *confine.Root) *DirReader {
	return &DirReader{Label: label, Root: root, MaxDepth: 8, MaxEntries: 5000}
}

func (d *DirReader) Name() string         { return d.Label }
func (d *DirReader) Tier(p string) string { return outsideTier(p) }

// Skipped lists what the last List dropped, in path order: directories it did not enter,
// links, and files it could not open.
func (d *DirReader) Skipped() []Skip { return d.skipped }

func (d *DirReader) skip(p, reason string) {
	d.skipped = append(d.skipped, Skip{Path: p, Reason: reason})
}

func (d *DirReader) List(ctx context.Context) ([]Entry, error) {
	d.skipped = nil
	var es []Entry
	var walk func(rel string, depth int) error
	walk = func(rel string, depth int) error {
		dir := rel
		if dir == "" {
			dir = "."
		}
		ents, err := d.Root.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, e := range ents {
			if err := ctx.Err(); err != nil {
				return err
			}
			if e.Name() == ".git" {
				continue
			}
			child := path.Join(rel, e.Name())
			if e.Type()&fs.ModeSymlink != 0 {
				d.skip(child, DispLink)
				continue
			}
			if e.IsDir() {
				if depth+1 < d.MaxDepth {
					if err := walk(child, depth+1); err != nil {
						return err
					}
				} else {
					d.skip(child+"/", DispTooDeep)
				}
				continue
			}
			if !e.Type().IsRegular() {
				d.skip(child, DispUnreadable)
				continue
			}
			// DirEntry.Info does not work on an entry read through a directory handle, so size
			// comes from opening the file through the root, which also refuses links, special
			// files and files with extra hard links.
			f, err := d.Root.OpenRead(child)
			if err != nil {
				d.skip(child, DispUnreadable)
				continue
			}
			info, err := f.Stat()
			f.Close()
			if err != nil || !info.Mode().IsRegular() {
				d.skip(child, DispUnreadable)
				continue
			}
			if len(es) >= d.MaxEntries {
				return fmt.Errorf("bootstrap: %s holds more than %d files; narrow the --source directory", d.Label, d.MaxEntries)
			}
			es = append(es, Entry{Source: d.Label, Path: child, Size: info.Size(), Modified: info.ModTime()})
		}
		return nil
	}
	if err := walk("", 0); err != nil {
		return nil, err
	}
	sort.Slice(es, func(i, j int) bool { return es[i].Path < es[j].Path })
	sort.Slice(d.skipped, func(i, j int) bool { return d.skipped[i].Path < d.skipped[j].Path })
	return es, nil
}
func (d *DirReader) Read(_ context.Context, e Entry, max int64) ([]byte, error) {
	return d.Root.ReadFile(e.Path, max)
}
