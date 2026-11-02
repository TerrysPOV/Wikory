package app

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/retrieve"
)

func init() {
	register(Command{Name: "search", Summary: "search this checkout's wiki", Run: runSearch})
}

type hitJSON struct {
	RelPath   string   `json:"relpath"`
	ClaimID   string   `json:"claim_id,omitempty"`
	Text      string   `json:"text"`
	Status    string   `json:"status,omitempty"`
	Freshness string   `json:"freshness,omitempty"`
	Notes     []string `json:"notes,omitempty"`
	PageOnly  bool     `json:"page_only"`
	Score     float64  `json:"score"`
}

func runSearch(ctx context.Context, env *Env, args []string) (Result, error) {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 10, "maximum results")
	history := fs.Bool("history", false, "include superseded claims")
	if err := fs.Parse(args); err != nil {
		return Result{}, &UsageError{Msg: err.Error()}
	}
	query := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(query) == "" {
		return Result{}, &UsageError{Msg: "usage: wikory search [--limit N] [--history] <terms>"}
	}
	p, err := openProject(ctx, env, false)
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	if !servable(p) {
		return Result{}, errors.New(strings.TrimSpace(strings.SplitN(notRegisteredBriefing(p), "\n", 2)[0]))
	}
	prog := NewProgress(env.IO.Err, env.JSON, false, time.Now)
	stats, err := p.refresh(ctx, prog)
	if err != nil {
		return Result{}, err
	}
	hits, err := retrieve.Search(ctx, p.Store.DB, p.Checkout.ID, p.RepoRoot, p.validateEnv(), query, retrieve.SearchOpts{Limit: *limit, History: *history})
	if err != nil {
		return Result{}, err
	}
	out := make([]hitJSON, 0, len(hits))
	for _, h := range hits {
		out = append(out, hitJSON{RelPath: h.RelPath, ClaimID: h.ClaimID, Text: h.Text, Status: h.Display.Status,
			Freshness: h.Display.Freshness, Notes: h.Display.Notes, PageOnly: h.PageOnly, Score: h.Score})
	}
	// The index's own notices go into the rendered text, so a partial index is never shown as complete.
	return Result{Text: retrieve.RenderHits(hits, stats.Notices...), Notices: p.Notices,
		Data: map[string]any{"preface": retrieve.Preface, "query": query, "hits": out,
			"index_partial": stats.Partial, "index_notices": stats.Notices}}, nil
}
