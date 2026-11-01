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

type walkState struct {
	rels     []string
	refused  int
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
		}
	}
	return nil
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
	tx, err := ix.DB.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	present := map[string]bool{}
	var total int64
	byteLimit := false
	for i, rel := range rels {
		if err := ctx.Err(); err != nil {
			return st, err
		}
		if byteLimit {
			present[rel] = true // not read, so not deleted either
			st.Skipped++
			continue
		}
		data, err := ix.Repo.ReadFile(rel, maxPage)
		if err != nil {
			st.Refused++
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
		if known[rel] != ids.SHA256Hex(data) {
			n, err := ix.indexPage(ctx, tx, rel, data)
			if err != nil {
				return st, err
			}
			st.Malformed += n
			st.Changed++
		}
		if ix.Progress != nil {
			ix.Progress(i+1, len(rels))
		}
	}
	if !w.cutShort {
		for rel := range known {
			if !present[rel] {
				if err := deletePage(ctx, tx, ix.CheckoutID, rel); err != nil {
					return st, err
				}
				st.Removed++
			}
		}
	}
	if st.Skipped > 0 {
		st.Partial = true
		st.Notices = append(st.Notices, fmt.Sprintf("The memory index is partial: %d page(s) or director(ies) were skipped because a size limit was reached (%d pages, %d bytes, depth %d).", st.Skipped, maxPages, maxBytes, maxDepth))
	}
	return st, tx.Commit()
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
