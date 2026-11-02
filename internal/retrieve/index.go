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
	"time"

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
	// ParseBudget is the most time a refresh spends parsing changed pages (zero means
	// DefaultParseBudget). Now is the clock that measures it (zero means time.Now).
	ParseBudget time.Duration
	Now         func() time.Time

	// Per-refresh counters filled by collect and read by notices.
	refusedN, malformedN, degradedN nameList
	degradedWhy                     string
	// afterCollect, if set, runs between the two phases. Tests only.
	afterCollect func()
}

// RefreshStats reports what a refresh did. Skipped counts pages and directories that a limit
// kept out (a lower bound: the walk stops at the page limit). Partial is true whenever Skipped
// is above zero: the index then covers only part of the tree. Refused counts entries that were
// refused individually (symlinks, unreadable directories, unreadable files).
type RefreshStats struct {
	Pages, Changed, Removed, Refused, Malformed int
	Budgeted                                    int // changed pages indexed by title only for want of parse time
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

// DefaultParseBudget is the most time one refresh spends parsing pages. Pages beyond it are
// indexed by title only and retried by the next refresh.
const DefaultParseBudget = 1500 * time.Millisecond

// known is what the index already holds for one page.
type known struct {
	hash      string
	malformed int // -1: no page_notes row yet (an index from before notes), so reparse
	degraded  string
}

// pageWork is one page of a refresh after phase 1. page is nil for an unchanged page, whose
// malformed count and degraded reason come from the index.
type pageWork struct {
	rel, hash string
	page      *claim.Page
	malformed int
	degraded  string
	retry     bool // indexed by title only for want of parse time: stored with an empty hash
}

// Refresh makes the index match the tree. It works in two phases. Phase 1, outside any lock,
// walks the tree, reads and hashes pages and parses the ones that changed, within the limits
// and the parse time budget. Phase 2 takes the database write lock and only writes what
// phase 1 collected, so a slow parse never holds up another session's write.
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
	ix.refusedN, ix.malformedN, ix.degradedN, ix.degradedWhy = w.refusedN, nameList{}, nameList{}, ""
	rels := w.rels
	sort.Strings(rels)
	have := map[string]known{}
	rows, err := ix.DB.QueryContext(ctx, `SELECT p.relpath, p.content_hash, COALESCE(n.malformed, -1), COALESCE(n.degraded, '')
		FROM pages p LEFT JOIN page_notes n ON n.checkout_id = p.checkout_id AND n.relpath = p.relpath WHERE p.checkout_id = ?`, ix.CheckoutID)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var r string
		var k known
		if err := rows.Scan(&r, &k.hash, &k.malformed, &k.degraded); err != nil {
			rows.Close()
			return st, err
		}
		have[r] = k
	}
	rows.Close()

	work, present, err := ix.collect(ctx, rels, have, maxBytes, &st)
	if err != nil {
		return st, err
	}
	if ix.afterCollect != nil {
		ix.afterCollect()
	}
	err = ix.write(ctx, func(ctx context.Context) error {
		tx, err := ix.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, it := range work {
			if it.page == nil {
				continue
			}
			if err := ix.indexPage(ctx, tx, it); err != nil {
				return err
			}
		}
		if !w.cutShort {
			for rel := range have {
				if !present[rel] {
					if err := deletePage(ctx, tx, ix.CheckoutID, rel); err != nil {
						return err
					}
					st.Removed++
				}
			}
		}
		return tx.Commit()
	})
	if err != nil {
		return st, err
	}
	ix.notices(&st, maxPages, maxBytes, maxDepth)
	return st, nil
}

// collect is phase 1: it reads every listed page within the byte limit, hashes it, and parses
// the changed ones until the parse budget is spent. It returns the work for phase 2 and the set
// of paths present in the tree (including those skipped by a limit, which are not deleted).
func (ix *Indexer) collect(ctx context.Context, rels []string, have map[string]known, maxBytes int64, st *RefreshStats) ([]pageWork, map[string]bool, error) {
	now := ix.Now
	if now == nil {
		now = time.Now
	}
	budget := ix.ParseBudget
	if budget <= 0 {
		budget = DefaultParseBudget
	}
	var spent time.Duration
	var work []pageWork
	present := map[string]bool{}
	var total int64
	byteLimit := false
	for i, rel := range rels {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if byteLimit {
			present[rel] = true // not read, so not deleted either
			st.Skipped++
			continue
		}
		data, err := ix.Repo.ReadFile(rel, maxPage)
		if err != nil {
			st.Refused++ // over the page size cap, unreadable or swapped for a symlink
			ix.refusedN.add(rel)
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
		hash := ids.SHA256Hex(data)
		it := pageWork{rel: rel, hash: hash}
		if k, ok := have[rel]; ok && k.hash == hash && k.malformed >= 0 {
			it.malformed, it.degraded = k.malformed, k.degraded // unchanged: no parse
		} else {
			var p claim.Page
			if spent > budget {
				p = claim.ParsePageTitleOnly(rel, data, "parse time budget used up; only its title is indexed until the next refresh")
				it.retry = true
				st.Budgeted++
			} else {
				began := now()
				p = claim.ParsePage(rel, data)
				spent += now().Sub(began)
			}
			it.page, it.malformed, it.degraded = &p, malformedClaims(p), p.Degraded
			st.Changed++
		}
		if it.malformed > 0 {
			st.Malformed += it.malformed
			ix.malformedN.add(rel)
		}
		if it.degraded != "" && !it.retry {
			ix.degradedN.add(rel)
			if ix.degradedWhy == "" {
				ix.degradedWhy = it.degraded
			}
		}
		work = append(work, it)
		if ix.Progress != nil {
			ix.Progress(i+1, len(rels))
		}
	}
	return work, present, nil
}

// notices fills the refresh notices from what phase 1 counted.
func (ix *Indexer) notices(st *RefreshStats, maxPages int, maxBytes int64, maxDepth int) {
	if ix.refusedN.total > 0 {
		st.Notices = append(st.Notices, fmt.Sprintf("%d wiki file(s) or director(ies) were not indexed, so search and the briefing cannot see them: a symlink, an unreadable file or directory, or a page over %d bytes: %s.", ix.refusedN.total, maxPage, ix.refusedN.describe()))
	}
	if st.Malformed > 0 {
		st.Notices = append(st.Notices, fmt.Sprintf("%d claim(s) in %d page(s) were left out of the index because their metadata comment is malformed: %s.", st.Malformed, ix.malformedN.total, ix.malformedN.describe()))
	}
	if ix.degradedN.total > 0 {
		st.Notices = append(st.Notices, fmt.Sprintf("%d page(s) are indexed only in part, so search and the briefing cannot see all of their text (%s): %s.", ix.degradedN.total, singleLine(truncateRunes(ix.degradedWhy, 120)), ix.degradedN.describe()))
	}
	if st.Budgeted > 0 {
		st.Partial = true
		st.Notices = append(st.Notices, fmt.Sprintf("The memory index is partial: the parse time budget ran out, so %d changed page(s) are indexed by title only until the next refresh.", st.Budgeted))
	}
	if st.Skipped > 0 {
		st.Partial = true
		st.Notices = append(st.Notices, fmt.Sprintf("The memory index is partial: %d page(s) or director(ies) were skipped because a size limit was reached (%d pages, %d bytes, depth %d).", st.Skipped, maxPages, maxBytes, maxDepth))
	}
}

func deletePage(ctx context.Context, tx *sql.Tx, checkout, rel string) error {
	for _, q := range []string{
		`DELETE FROM fts WHERE checkout_id = ? AND relpath = ?`,
		`DELETE FROM claims WHERE checkout_id = ? AND relpath = ?`,
		`DELETE FROM page_notes WHERE checkout_id = ? AND relpath = ?`,
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

// indexPage writes one parsed page: the page row (with an empty hash when it is to be retried),
// its notes, its body and its claims.
func (ix *Indexer) indexPage(ctx context.Context, tx *sql.Tx, it pageWork) error {
	rel, p := it.rel, *it.page
	if err := deletePage(ctx, tx, ix.CheckoutID, rel); err != nil {
		return err
	}
	hash := it.hash
	if it.retry {
		hash = "" // never equal to a real hash, so the next refresh parses the page in full
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pages(checkout_id, relpath, content_hash, page_id, title, type) VALUES(?, ?, ?, ?, ?, ?)`,
		ix.CheckoutID, rel, hash, p.ID, p.Title, p.Type); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO page_notes(checkout_id, relpath, malformed, degraded) VALUES(?, ?, ?, ?)`,
		ix.CheckoutID, rel, it.malformed, it.degraded); err != nil {
		return err
	}
	pathWords := strings.TrimSuffix(path.Base(rel), ".md")
	if _, err := tx.ExecContext(ctx, `INSERT INTO fts(title, heading, claim, body, search_text, checkout_id, relpath, ordinal) VALUES(?, '', '', ?, ?, ?, ?, -1)`,
		p.Title, p.Body, Expand(p.Title+" "+pathWords+" "+p.Body), ix.CheckoutID, rel); err != nil {
		return err
	}
	for i, b := range p.Blocks {
		if b.Err != nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO claims(checkout_id, relpath, ordinal, claim_id, meta, text, heading, status, kind) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ix.CheckoutID, rel, i, b.Claim.ID, b.MetaLine, b.Text, b.Heading, b.Claim.Status, b.Claim.Kind); err != nil {
			return err
		}
		extra := strings.Join(b.Claim.AKA, " ") + " " + strings.Join(b.Claim.Deps, " ")
		if _, err := tx.ExecContext(ctx, `INSERT INTO fts(title, heading, claim, body, search_text, checkout_id, relpath, ordinal) VALUES(?, ?, ?, '', ?, ?, ?, ?)`,
			p.Title, b.Heading, b.Text, Expand(p.Title+" "+b.Heading+" "+b.Text+" "+extra), ix.CheckoutID, rel, i); err != nil {
			return err
		}
	}
	return nil
}
