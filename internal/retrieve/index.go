package retrieve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/store"
)

// WikiDir is where committed pages live.
const WikiDir = "project-memory/wiki"

const maxPage = 1 << 20

// Default limits. They bound the work and the memory one refresh can spend on a hostile or
// enormous tree. Memory is bounded by one page (maxPage) because each page is indexed as soon as
// it is read. A limit that is hit never fails the refresh: it is counted in RefreshStats.Skipped,
// described in RefreshStats.Notices, and sets RefreshStats.Partial so the index is never presented
// as complete.
const (
	// DefaultMaxPages is the most pages one refresh indexes.
	DefaultMaxPages = 5000
	// DefaultMaxTotalBytes is the most page bytes one refresh reads (64 MiB).
	DefaultMaxTotalBytes int64 = 64 << 20
	// DefaultMaxDepth is the deepest directory below the wiki root that is walked.
	DefaultMaxDepth = 16
)

// Indexer keeps one checkout's index equal to the pages present in its tree.
// MaxPages, MaxTotalBytes and MaxDepth override the defaults when above zero.
type Indexer struct {
	DB            *sql.DB
	CheckoutID    string
	Repo          *confine.Root
	Progress      func(done, total int)
	MaxPages      int
	MaxTotalBytes int64
	MaxDepth      int
	// WriteLockPath is the store's database write lock file (store.Store.WriteLockPath). The
	// refresh transaction runs inside it. Empty means no cross-process lock, which only a
	// caller with a private database (a test) may want.
	WriteLockPath string
}

// RefreshStats reports what a refresh did. Skipped counts pages and directories that a limit
// kept out (a lower bound: the walk stops at the page limit). Partial is true whenever Skipped
// is above zero: the index then covers only part of the tree. Refused counts entries that were
// refused individually (symlinks, unreadable directories, unreadable files).
type RefreshStats struct {
	Pages, Changed, Removed, Refused, Malformed int
	Skipped                                     int
	Partial                                     bool
	Notices                                     []string
}

// maxNoticeNames is how many page names a notice lists; the rest are counted.
const maxNoticeNames = 5

// nameList counts names and keeps the first few for a notice.
type nameList struct {
	total int
	names []string
}

func (l *nameList) add(rel string) {
	l.total++
	if len(l.names) < maxNoticeNames {
		l.names = append(l.names, rel)
	}
}

// describe renders the kept names, sanitised to one line and without the wiki prefix, then
// says how many more there were.
func (l *nameList) describe() string {
	shown := make([]string, len(l.names))
	for i, n := range l.names {
		shown[i] = truncateRunes(singleLine(strings.TrimPrefix(n, WikiDir+"/")), 80)
	}
	out := strings.Join(shown, ", ")
	if more := l.total - len(l.names); more > 0 {
		out += fmt.Sprintf(" and %d more", more)
	}
	return out
}

type walkState struct {
	rels     []string
	refused  int
	refusedN nameList
	skipped  int
	cutShort bool // the page list is incomplete: unseen pages must not be treated as deleted
	maxPages int
	maxDepth int
}

// walk lists regular .md files under the wiki. Entries it cannot use are refused and counted;
// only context cancellation stops it.
func (ix *Indexer) walk(ctx context.Context, dir string, depth int, w *walkState) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > w.maxDepth {
		w.skipped++
		w.cutShort = true
		return nil
	}
	ents, err := ix.Repo.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		w.refused++ // symlink, escape, not a directory, permission denied
		w.refusedN.add(dir)
		return nil
	}
	for _, e := range ents {
		name := e.Name()
		rel := dir + "/" + name
		switch {
		case strings.HasPrefix(name, "."):
			continue
		case e.IsDir():
			if name == "_archive" {
				continue
			}
			if err := ix.walk(ctx, rel, depth+1, w); err != nil {
				return err
			}
		case e.Type().IsRegular() && strings.HasSuffix(name, ".md"):
			if len(w.rels) >= w.maxPages {
				w.skipped++
				w.cutShort = true
				return nil
			}
			w.rels = append(w.rels, rel)
		case !e.Type().IsRegular():
			w.refused++ // symlinks, junctions, devices, FIFOs
			w.refusedN.add(rel)
		}
	}
	return nil
}

// write runs fn, the refresh transaction, inside the database write lock when one is set.
func (ix *Indexer) write(ctx context.Context, fn func(ctx context.Context) error) error {
	if ix.WriteLockPath == "" {
		return fn(ctx)
	}
	return store.WithWriteLockAt(ctx, ix.WriteLockPath, fn)
}

// Refresh makes the index match the tree.
func (ix *Indexer) Refresh(ctx context.Context) (RefreshStats, error) {
	var st RefreshStats
	maxPages, maxBytes, maxDepth := ix.MaxPages, ix.MaxTotalBytes, ix.MaxDepth
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxTotalBytes
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	w := &walkState{maxPages: maxPages, maxDepth: maxDepth}
	if err := ix.walk(ctx, WikiDir, 0, w); err != nil {
		return st, err
	}
	st.Refused, st.Skipped = w.refused, w.skipped
	refusedN := w.refusedN
	var malformedN nameList // pages with at least one malformed claim
	rels := w.rels
	sort.Strings(rels)
	known := map[string]string{}
	rows, err := ix.DB.QueryContext(ctx, `SELECT relpath, content_hash FROM pages WHERE checkout_id = ?`, ix.CheckoutID)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var r, h string
		if err := rows.Scan(&r, &h); err != nil {
			rows.Close()
			return st, err
		}
		known[r] = h
	}
	rows.Close()
	// The whole transaction, file reads included, runs inside the cross-process database write
	// lock, so the index is rewritten by one process at a time on every platform.
	err = ix.write(ctx, func(ctx context.Context) error {
		tx, err := ix.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		present := map[string]bool{}
		var total int64
		byteLimit := false
		for i, rel := range rels {
			if err := ctx.Err(); err != nil {
				return err
			}
			if byteLimit {
				present[rel] = true // not read, so not deleted either
				st.Skipped++
				continue
			}
			data, err := ix.Repo.ReadFile(rel, maxPage)
			if err != nil {
				st.Refused++ // over the page size cap, unreadable or swapped for a symlink
				refusedN.add(rel)
				continue
			}
			present[rel] = true
			if total+int64(len(data)) > maxBytes {
				byteLimit = true
				st.Skipped++
				continue
			}
			total += int64(len(data))
			st.Pages++
			var n int
			if known[rel] != ids.SHA256Hex(data) {
				if n, err = ix.indexPage(ctx, tx, rel, data); err != nil {
					return err
				}
				st.Changed++
			} else {
				n = malformedClaims(claim.ParsePage(rel, data)) // unchanged pages are still disclosed
			}
			if n > 0 {
				st.Malformed += n
				malformedN.add(rel)
			}
			if ix.Progress != nil {
				ix.Progress(i+1, len(rels))
			}
		}
		if !w.cutShort {
			for rel := range known {
				if !present[rel] {
					if err := deletePage(ctx, tx, ix.CheckoutID, rel); err != nil {
						return err
					}
					st.Removed++
				}
			}
		}
		if refusedN.total > 0 {
			st.Notices = append(st.Notices, fmt.Sprintf("%d wiki file(s) or director(ies) were not indexed, so search and the briefing cannot see them: a symlink, an unreadable file or directory, or a page over %d bytes: %s.", refusedN.total, maxPage, refusedN.describe()))
		}
		if st.Malformed > 0 {
			st.Notices = append(st.Notices, fmt.Sprintf("%d claim(s) in %d page(s) were left out of the index because their metadata comment is malformed: %s.", st.Malformed, malformedN.total, malformedN.describe()))
		}
		if st.Skipped > 0 {
			st.Partial = true
			st.Notices = append(st.Notices, fmt.Sprintf("The memory index is partial: %d page(s) or director(ies) were skipped because a size limit was reached (%d pages, %d bytes, depth %d).", st.Skipped, maxPages, maxBytes, maxDepth))
		}
		return tx.Commit()
	})
	return st, err
}

func deletePage(ctx context.Context, tx *sql.Tx, checkout, rel string) error {
	for _, q := range []string{
		`DELETE FROM fts WHERE checkout_id = ? AND relpath = ?`,
		`DELETE FROM claims WHERE checkout_id = ? AND relpath = ?`,
		`DELETE FROM pages WHERE checkout_id = ? AND relpath = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, checkout, rel); err != nil {
			return err
		}
	}
	return nil
}

func malformedClaims(p claim.Page) int {
	n := 0
	for _, b := range p.Blocks {
		if b.Err != nil {
			n++
		}
	}
	return n
}

func (ix *Indexer) indexPage(ctx context.Context, tx *sql.Tx, rel string, data []byte) (int, error) {
	if err := deletePage(ctx, tx, ix.CheckoutID, rel); err != nil {
		return 0, err
	}
	p := claim.ParsePage(rel, data)
	if _, err := tx.ExecContext(ctx, `INSERT INTO pages(checkout_id, relpath, content_hash, page_id, title, type) VALUES(?, ?, ?, ?, ?, ?)`,
		ix.CheckoutID, rel, ids.SHA256Hex(data), p.ID, p.Title, p.Type); err != nil {
		return 0, err
	}
	pathWords := strings.TrimSuffix(path.Base(rel), ".md")
	if _, err := tx.ExecContext(ctx, `INSERT INTO fts(title, heading, claim, body, search_text, checkout_id, relpath, ordinal) VALUES(?, '', '', ?, ?, ?, ?, -1)`,
		p.Title, p.Body, Expand(p.Title+" "+pathWords+" "+p.Body), ix.CheckoutID, rel); err != nil {
		return 0, err
	}
	malformed := 0
	for i, b := range p.Blocks {
		if b.Err != nil {
			malformed++
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO claims(checkout_id, relpath, ordinal, claim_id, meta, text, heading, status, kind) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ix.CheckoutID, rel, i, b.Claim.ID, b.MetaLine, b.Text, b.Heading, b.Claim.Status, b.Claim.Kind); err != nil {
			return 0, err
		}
		extra := strings.Join(b.Claim.AKA, " ") + " " + strings.Join(b.Claim.Deps, " ")
		if _, err := tx.ExecContext(ctx, `INSERT INTO fts(title, heading, claim, body, search_text, checkout_id, relpath, ordinal) VALUES(?, ?, ?, '', ?, ?, ?, ?)`,
			p.Title, b.Heading, b.Text, Expand(p.Title+" "+b.Heading+" "+b.Text+" "+extra), ix.CheckoutID, rel, i); err != nil {
			return 0, err
		}
	}
	return malformed, nil
}
