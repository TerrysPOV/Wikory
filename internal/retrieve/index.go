package retrieve

import (
	"context"
	"database/sql"
	"errors"
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

// Indexer keeps one checkout's index equal to the pages present in its tree.
type Indexer struct {
	DB         *sql.DB
	CheckoutID string
	Repo       *confine.Root
	Progress   func(done, total int)
}

// RefreshStats reports what a refresh did.
type RefreshStats struct {
	Pages, Changed, Removed, Refused, Malformed int
}

type found struct {
	rel  string
	data []byte
}

// walk lists regular .md files under the wiki, refusing anything else.
func (ix *Indexer) walk(dir string, out *[]string, refused *int) error {
	ents, err := ix.Repo.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
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
			if err := ix.walk(rel, out, refused); err != nil {
				if errors.Is(err, confine.ErrSymlink) {
					*refused++
					continue
				}
				return err
			}
		case e.Type().IsRegular() && strings.HasSuffix(name, ".md"):
			*out = append(*out, rel)
		case !e.Type().IsRegular():
			*refused++ // symlinks, junctions, devices, FIFOs
		}
	}
	return nil
}

// Refresh makes the index match the tree.
func (ix *Indexer) Refresh(ctx context.Context) (RefreshStats, error) {
	var st RefreshStats
	var rels []string
	if err := ix.walk(WikiDir, &rels, &st.Refused); err != nil {
		return st, err
	}
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
	var changed []found
	present := map[string]bool{}
	for i, rel := range rels {
		data, err := ix.Repo.ReadFile(rel, maxPage)
		if err != nil {
			st.Refused++
			continue
		}
		present[rel] = true
		st.Pages++
		if known[rel] != ids.SHA256Hex(data) {
			changed = append(changed, found{rel, data})
		}
		if ix.Progress != nil {
			ix.Progress(i+1, len(rels))
		}
	}
	tx, err := ix.DB.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	for rel := range known {
		if !present[rel] {
			if err := deletePage(ctx, tx, ix.CheckoutID, rel); err != nil {
				return st, err
			}
			st.Removed++
		}
	}
	for _, f := range changed {
		n, err := ix.indexPage(ctx, tx, f)
		if err != nil {
			return st, err
		}
		st.Malformed += n
		st.Changed++
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

func (ix *Indexer) indexPage(ctx context.Context, tx *sql.Tx, f found) (int, error) {
	if err := deletePage(ctx, tx, ix.CheckoutID, f.rel); err != nil {
		return 0, err
	}
	p := claim.ParsePage(f.rel, f.data)
	if _, err := tx.ExecContext(ctx, `INSERT INTO pages(checkout_id, relpath, content_hash, page_id, title, type) VALUES(?, ?, ?, ?, ?, ?)`,
		ix.CheckoutID, f.rel, ids.SHA256Hex(f.data), p.ID, p.Title, p.Type); err != nil {
		return 0, err
	}
	pathWords := strings.TrimSuffix(path.Base(f.rel), ".md")
	if _, err := tx.ExecContext(ctx, `INSERT INTO fts(title, heading, claim, body, search_text, checkout_id, relpath, ordinal) VALUES(?, '', '', ?, ?, ?, ?, -1)`,
		p.Title, p.Body, Expand(p.Title+" "+pathWords+" "+p.Body), ix.CheckoutID, f.rel); err != nil {
		return 0, err
	}
	malformed := 0
	for i, b := range p.Blocks {
		if b.Err != nil {
			malformed++
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO claims(checkout_id, relpath, ordinal, claim_id, meta, text, heading, status, kind) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			ix.CheckoutID, f.rel, i, b.Claim.ID, b.MetaLine, b.Text, b.Heading, b.Claim.Status, b.Claim.Kind); err != nil {
			return 0, err
		}
		extra := strings.Join(b.Claim.AKA, " ") + " " + strings.Join(b.Claim.Deps, " ")
		if _, err := tx.ExecContext(ctx, `INSERT INTO fts(title, heading, claim, body, search_text, checkout_id, relpath, ordinal) VALUES(?, ?, ?, '', ?, ?, ?, ?)`,
			p.Title, b.Heading, b.Text, Expand(p.Title+" "+b.Heading+" "+b.Text+" "+extra), ix.CheckoutID, f.rel, i); err != nil {
			return 0, err
		}
	}
	return malformed, nil
}
