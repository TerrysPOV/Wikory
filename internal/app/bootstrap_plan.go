package app

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/gitx"
)

var sourceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

type planReport struct {
	RunID      string               `json:"run_id"`
	PlanHash   string               `json:"plan_hash"`
	Scope      string               `json:"scope"`
	Head       string               `json:"head"`
	Sample     int                  `json:"sample"`
	Sources    map[string]int       `json:"sources"`
	Components int                  `json:"components"`
	Inspected  int                  `json:"inspected"`
	Batches    int                  `json:"batches"`
	Estimates  []bootstrap.Estimate `json:"estimates"`
	// Roots is each --source name and the directory it reads, so the person's yes covers which
	// directories outside the repository are read.
	Roots map[string]string `json:"roots"`
	Next  string            `json:"next"`
}

func bootstrapPlan(ctx context.Context, env *Env, args []string) (Result, error) {
	fl, err := parseFlags(args, map[string]bool{"source": true, "sample": true})
	if err != nil {
		return Result{}, err
	}
	sample := 0
	if v := fl["sample"]; len(v) > 0 {
		if sample, err = strconv.Atoi(v[len(v)-1]); err != nil || sample < 1 {
			return Result{}, &UsageError{Msg: "--sample needs a whole number of at least 1"}
		}
	}
	p, err := openProject(ctx, env, false)
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	if !servable(p) {
		return Result{}, fmt.Errorf("this checkout is not registered and trusted; run wikory init")
	}
	if err := requireCleanDefaultBranch(ctx, p); err != nil {
		return Result{}, err
	}
	head, err := p.Repo.HeadCommit(ctx)
	if err != nil || head == "" {
		return Result{}, &UsageError{Msg: "the repository has no commits to baseline"}
	}
	readers := []bootstrap.Reader{bootstrap.NewRepoReader(p.Repo, head)}
	var notices []string
	scope := []string{"repo"}
	roots := map[string]string{}
	for _, spec := range fl["source"] {
		name, dir, ok := strings.Cut(spec, "=")
		if !ok || !sourceName.MatchString(name) || name == "repo" || name == bootstrap.UnreadSourceName || dir == "" {
			return Result{}, &UsageError{Msg: fmt.Sprintf("--source %q: want NAME=DIR with NAME like notes (lower case, not \"repo\" or \"git\")", spec)}
		}
		if _, dup := roots[name]; dup {
			return Result{}, &UsageError{Msg: fmt.Sprintf("--source %s was given twice; each source needs its own name", name)}
		}
		if strings.ContainsAny(dir, "\n\r") {
			return Result{}, &UsageError{Msg: fmt.Sprintf("--source %s: the directory name has a line break in it", name)}
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(p.Repo.Top, dir)
		}
		switch where := sourcePlace(p.Repo.Top, dir); where {
		case "project-memory":
			return Result{}, &UsageError{Msg: fmt.Sprintf("--source %s: %s is inside %s/, which is Wikory's own output; a bootstrap never reads its own wiki back as a source", name, spec[len(name)+1:], pmDir)}
		case "repository":
			notices = append(notices, fmt.Sprintf("--source %s is inside the repository, so its files are also read as repository files; a source is meant for material kept outside it", name))
		}
		roots[name] = dir
		root, err := confine.Open(dir)
		if err != nil {
			return Result{}, &UsageError{Msg: fmt.Sprintf("--source %s: cannot open %s: %v", name, spec[len(name)+1:], describeConfine(err))}
		}
		defer root.Close()
		readers = append(readers, bootstrap.NewDirReader(name, root))
		scope = append(scope, name)
	}
	sort.Strings(scope[1:]) // the plan hash covers the scope: the order the flags were typed in must not
	since, err := churnSince(ctx, p.Repo)
	if err != nil {
		return Result{}, err
	}
	churn, err := p.Repo.Churn(ctx, since)
	if err != nil {
		return Result{}, err
	}
	changed, err := p.Repo.LastChanged(ctx)
	if err != nil {
		return Result{}, err
	}
	branches, err := p.Repo.Branches(ctx)
	if err != nil {
		return Result{}, err
	}
	trees, err := p.Repo.Worktrees(ctx)
	if err != nil {
		return Result{}, err
	}
	prog := NewProgress(env.IO.Err, env.JSON, IsTTY(env.IO.Err), time.Now)
	started := false
	res, err := bootstrap.Inventory(ctx, readers, bootstrap.Options{MaxFileBytes: bootstrap.MaxFileBytes, Churn: churn, Budget: bootstrap.InspectBudget,
		LastChanged: changed, Unread: bootstrap.UnreadSources(p.Repo.DefaultBranch(ctx), branches, trees),
		Progress: func(done, total int) {
			if !started {
				started = true
				prog.Begin("inventory", total, time.Duration(total/200)*time.Second, "Reading and scanning "+strconv.Itoa(total)+" files (estimate, not yet measured). Nothing is sent anywhere.")
			}
			prog.Step(done)
		}})
	prog.End()
	if err != nil {
		return Result{}, err
	}
	wf := bootstrap.WorkFiles{Root: p.Store.Root, CheckoutID: p.Checkout.ID}
	prep, err := bootstrap.PrepareWork(ctx, readers, res, wf, nil)
	if err != nil {
		return Result{}, err
	}
	if prep.Redactions > 0 {
		notices = append(notices, fmt.Sprintf("%d value(s) were redacted while preparing work files; check the sources for secrets", prep.Redactions))
	}
	plan := bootstrap.BuildPlan(res, strings.Join(scope, "+"), head, sample)
	plan.Roots = roots
	plan.Branch = branchLabel(ctx, p)
	run, err := newRuns(p).Create(ctx, plan, bootstrap.ExtractSpecs(res))
	if err != nil {
		return Result{}, err
	}
	rep := planReport{RunID: run.ID, PlanHash: plan.Hash, Scope: plan.Scope, Head: head, Sample: sample,
		Sources: map[string]int{}, Components: len(res.Components), Batches: plan.Batches, Estimates: plan.Estimates, Roots: roots}
	for _, s := range res.Sources {
		rep.Sources[s.Disposition]++
	}
	for _, c := range res.Components {
		if c.Inspected {
			rep.Inspected++
		}
	}
	rep.Next = fmt.Sprintf("wikory bootstrap confirm --run %s --plan-hash %s", run.ID, plan.Hash)
	if sample > 0 {
		rep.Next += " --sample" // a run planned with a sample cannot start in full until its sample has run
	}
	return Result{Data: rep, Text: planText(rep), Notices: notices}, nil
}

// sourcePlace says where a --source directory sits: "project-memory" when it resolves to Wikory's
// own output directory or anything under it, "repository" when it is elsewhere inside the
// checkout, and "" when it is outside. Links are resolved first, so a link cannot hide where a
// directory really is; a directory that cannot be resolved is left to confine.Open to refuse.
func sourcePlace(top, dir string) string {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return ""
	}
	root, err := filepath.EvalSymlinks(top)
	if err != nil {
		root = top
	}
	within := func(base string) bool {
		rel, err := filepath.Rel(base, resolved)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	switch {
	case within(filepath.Join(root, pmDir)):
		return "project-memory"
	case within(root):
		return "repository"
	}
	return ""
}

// branchLabel is the branch the checkout is on, or "detached" when HEAD names no branch.
func branchLabel(ctx context.Context, p *project) string {
	if b := p.Repo.Branch(ctx); b != "" {
		return b
	}
	return "detached"
}

// churnWindow is how far back from HEAD's commit time churn counts commits.
const churnWindow = 90 * 24 * time.Hour

// churnSince anchors the churn window on HEAD's committer time, not the clock: churn feeds the
// ranking, the ranking is in the plan hash, and the same commit must plan the same on any day.
func churnSince(ctx context.Context, repo *gitx.Repo) (time.Time, error) {
	t, err := repo.HeadTime(ctx)
	if err != nil {
		return time.Time{}, err
	}
	return t.Add(-churnWindow), nil
}

// requireCleanDefaultBranch is the baseline precondition: bootstrap describes the default
// branch at HEAD, so a feature branch or uncommitted work would make the baseline say things
// the default branch does not. Uncommitted changes under project-memory/ are ignored: that is
// where Wikory itself writes.
func requireCleanDefaultBranch(ctx context.Context, p *project) error {
	def, cur := p.Repo.DefaultBranch(ctx), p.Repo.Branch(ctx)
	if def == "" || cur != def {
		return &UsageError{Msg: fmt.Sprintf("bootstrap baselines the default branch (%q) and this checkout is on %q; switch to a clean checkout of the default branch", def, cur)}
	}
	dirty, err := p.Repo.Dirty(ctx, ".")
	if err != nil {
		return err
	}
	var real []string
	for _, d := range dirty {
		if !strings.HasPrefix(d, pmDir+"/") {
			real = append(real, d)
		}
	}
	if len(real) > 0 {
		sort.Strings(real)
		return &UsageError{Msg: fmt.Sprintf("the working tree has uncommitted changes (first: %s); commit or stash them so the baseline matches HEAD", real[0])}
	}
	return nil
}

func planText(r planReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Bootstrap plan %s (scope %s, HEAD %.12s)\n", r.RunID, r.Scope, r.Head)
	keys := make([]string, 0, len(r.Sources))
	for k := range r.Sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	b.WriteString("Sources:")
	for _, k := range keys {
		fmt.Fprintf(&b, " %s=%d", k, r.Sources[k])
	}
	fmt.Fprintf(&b, "\nComponents: %d found, %d to be verified\n", r.Components, r.Inspected)
	if len(r.Roots) > 0 {
		names := make([]string, 0, len(r.Roots))
		for n := range r.Roots {
			names = append(names, n)
		}
		sort.Strings(names)
		b.WriteString("Directories outside the repository that will be read:\n")
		for _, n := range names {
			fmt.Fprintf(&b, "  --source %s=%s\n", n, r.Roots[n])
		}
	}
	if n := r.Sources[bootstrap.DispUnmergedBranch]; n > 0 || r.Sources[bootstrap.DispOtherWorktree] > 0 {
		fmt.Fprintf(&b, "Not read: %d branch(es) with unmerged commits and %d other worktree(s). The baseline is the default branch at HEAD; work that lives elsewhere is not in it.\n", n, r.Sources[bootstrap.DispOtherWorktree])
	}
	if n := r.Sources[bootstrap.DispTooDeep] + r.Sources[bootstrap.DispUnreadable] + r.Sources[bootstrap.DispLink]; n > 0 {
		fmt.Fprintf(&b, "Not read: %d item(s) in the --source directories were too deep, unreadable or links; finish lists each by name.\n", n)
	}
	for _, e := range r.Estimates {
		fmt.Fprintf(&b, "  %-8s %4d items, about %d input and %d output tokens (estimate, bytes/4; not yet measured)\n", e.Stage, e.Items, e.InputTokens, e.OutputTokens)
	}
	fmt.Fprintf(&b, "Batches: %d\n", r.Batches)
	b.WriteString("No per-token price is shown: none is configured, and on a subscription plan there is none.\n")
	fmt.Fprintf(&b, "Plan hash: %s\n", r.PlanHash)
	b.WriteString("Nothing has been sent to a model. A person must say yes before any paid step.\n")
	if r.Sample > 0 {
		b.WriteString("  To sample (extraction only):  " + r.Next + "\n")
		b.WriteString("  To run in full:   once the sample's measurements are in, the same command without --sample\n")
	} else {
		b.WriteString("  To sample first:  wikory bootstrap plan --sample N, then confirm --run RUN --plan-hash HASH --sample\n")
		b.WriteString("  To run in full:   " + r.Next + "\n")
	}
	return b.String()
}
