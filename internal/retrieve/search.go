package retrieve

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/validate"
)

// Hit is one search result.
type Hit struct {
	RelPath         string
	PageTitle       string
	Heading         string
	ClaimID         string
	Text            string
	Kind            string
	CommittedStatus string
	PageOnly        bool
	Score           float64
	Display         validate.Display
}

// SearchOpts tune a search.
type SearchOpts struct {
	Limit   int
	History bool
}

func matchExpr(words []string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = `"` + w + `"`
	}
	return strings.Join(q, " OR ")
}

// Search ranks this checkout's claims and pages for query.
func Search(ctx context.Context, db *sql.DB, checkoutID string, repo *confine.Root, env validate.Env, query string, opts SearchOpts) ([]Hit, error) {
	words := QueryWords(query)
	if len(words) == 0 {
		return nil, nil
	}
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	rows, err := db.QueryContext(ctx, `
		SELECT f.relpath, f.ordinal, p.title, COALESCE(c.heading, ''), COALESCE(c.claim_id, ''), COALESCE(c.text, f.body),
		       COALESCE(c.kind, ''), COALESCE(c.status, ''), COALESCE(c.meta, ''), bm25(fts, 10.0, 5.0, 3.0, 1.0, 2.0) AS score
		FROM fts f
		JOIN pages p ON p.checkout_id = f.checkout_id AND p.relpath = f.relpath
		LEFT JOIN claims c ON c.checkout_id = f.checkout_id AND c.relpath = f.relpath AND c.ordinal = f.ordinal
		WHERE fts MATCH ? AND f.checkout_id = ? AND (? OR COALESCE(c.status, '') <> 'superseded')
		ORDER BY score LIMIT ?`, matchExpr(words), checkoutID, opts.History, opts.Limit)
	if err != nil {
		return nil, err
	}
	var hits []Hit
	var metas []string
	for rows.Next() {
		var h Hit
		var ordinal int
		var meta string
		if err := rows.Scan(&h.RelPath, &ordinal, &h.PageTitle, &h.Heading, &h.ClaimID, &h.Text, &h.Kind, &h.CommittedStatus, &meta, &h.Score); err != nil {
			rows.Close()
			return nil, err
		}
		h.PageOnly = ordinal < 0
		hits = append(hits, h)
		metas = append(metas, meta)
	}
	rows.Close()
	var out []Hit
	for i, h := range hits {
		f, err := repo.OpenRead(h.RelPath) // scope: the file must still be present in this tree
		if err != nil {
			continue
		}
		f.Close()
		if !h.PageOnly {
			c, err := claim.ParseComment(metas[i])
			if err != nil {
				continue
			}
			if h.Display, err = validate.Derive(ctx, c, h.Text, env); err != nil {
				return nil, err
			}
		}
		h.Text = Sanitize(h.Text)
		if h.PageOnly {
			h.Text = truncateRunes(h.Text, 200)
		}
		h.PageTitle, h.Heading = Sanitize(h.PageTitle), Sanitize(h.Heading)
		h.RelPath, h.ClaimID = Sanitize(h.RelPath), Sanitize(h.ClaimID) // file names are page content too
		out = append(out, h)
	}
	return out, nil
}

// singleLine makes s safe for a single-line field: every control character and line separator
// becomes a space (so no field can start a new line), then Sanitize removes the markup.
func singleLine(s string) string {
	return Sanitize(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return ' '
		}
		return r
	}, s))
}

// lineBreaks turns every line separator a terminal or reader might honour into a newline.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u2028", "\n", "\u2029", "\n", "\u0085", "\n", "\v", "\n", "\f", "\n", "\x1c", "\n", "\x1d", "\n", "\x1e", "\n")

// continuation marks every line of a multi-line text after the first, so no line of retrieved
// text can begin with "- [" and pass for a result row.
const continuation = "  | "

func markedLines(s string) string {
	return strings.ReplaceAll(Sanitize(lineBreaks.Replace(s)), "\n", "\n"+continuation)
}

// RenderHits formats hits for people and agents, inside the delimited block. notices are
// the index's own warnings (RefreshStats.Notices, for example that the index is partial); they
// go before the block, so an incomplete index is never presented as complete. Every value is
// sanitised here, whatever the caller did: markup is removed, and only Text may span lines
// (each continuation line is marked).
func RenderHits(hits []Hit, notices ...string) string {
	var b strings.Builder
	b.WriteString(Preface + "\n")
	for _, n := range notices {
		b.WriteString(singleLine(n) + "\n")
	}
	b.WriteString("<wikory-memory>\n")
	if len(hits) == 0 {
		b.WriteString("No matches in this checkout.\n")
	}
	for _, h := range hits {
		if h.PageOnly {
			fmt.Fprintf(&b, "- [page] %s (%s): %s\n", singleLine(h.PageTitle), singleLine(h.RelPath), markedLines(h.Text))
			continue
		}
		label := singleLine(h.Display.Status)
		if h.Display.Freshness != "" {
			label += " | " + singleLine(h.Display.Freshness)
		}
		for _, n := range h.Display.Notes {
			label += " | " + singleLine(n)
		}
		fmt.Fprintf(&b, "- [%s] %s (%s#%s)\n", label, markedLines(h.Text), singleLine(h.RelPath), singleLine(h.ClaimID))
	}
	b.WriteString("</wikory-memory>\n")
	return b.String()
}
