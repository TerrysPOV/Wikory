package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

type nextItem struct {
	ID        string `json:"item"`
	Stage     string `json:"stage"`
	Ref       string `json:"ref"`
	Epoch     int    `json:"epoch"`
	WorkFile  string `json:"work_file"`
	WorkPath  string `json:"work_path"`
	InputHash string `json:"input_hash"`
}

type nextReport struct {
	RunID     string     `json:"run_id"`
	BatchID   string     `json:"batch"`
	Items     []nextItem `json:"items"`
	Remaining int        `json:"remaining"`
	Status    string     `json:"status"`
}

func openRun(ctx context.Context, env *Env, args []string, want map[string]bool) (*project, *bootstrap.Runs, map[string][]string, error) {
	fl, err := parseFlags(args, want)
	if err != nil {
		return nil, nil, nil, err
	}
	p, err := openProject(ctx, env, false)
	if err != nil {
		return nil, nil, nil, err
	}
	if !servable(p) {
		p.Close()
		return nil, nil, nil, errors.New("this checkout is not registered and trusted; run wikory init")
	}
	return p, newRuns(p), fl, nil
}

func oneFlag(fl map[string][]string, name string) string {
	if v := fl[name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return ""
}

func bootstrapNext(ctx context.Context, env *Env, args []string) (Result, error) {
	p, runs, fl, err := openRun(ctx, env, args, map[string]bool{"run": true, "max": true})
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	runID := oneFlag(fl, "run")
	if runID == "" {
		return Result{}, &UsageError{Msg: "usage: wikory bootstrap next --run RUN [--max N]"}
	}
	max := 0
	if v := oneFlag(fl, "max"); v != "" {
		if max, err = strconv.Atoi(v); err != nil || max < 1 {
			return Result{}, &UsageError{Msg: "--max needs a whole number of at least 1"}
		}
	}
	if err := prepareVerifyWithProgress(ctx, env, p, runs, runID); err != nil {
		return Result{}, err
	}
	b, err := runs.Next(ctx, runID, max)
	if err != nil {
		return Result{}, err
	}
	st, err := runs.Status(ctx, runID)
	if err != nil {
		return Result{}, err
	}
	rep := nextReport{RunID: runID, BatchID: b.ID, Items: []nextItem{}, Status: statusLine(st)}
	for _, stage := range st.Items {
		rep.Remaining += stage["pending"] + stage["leased"]
	}
	wf := bootstrap.WorkFiles{Root: p.Store.Root, CheckoutID: p.Checkout.ID}
	for _, it := range b.Items {
		rel := wf.Rel(it.InputHash)
		f, err := p.Store.Root.OpenRead(rel)
		if err != nil {
			return Result{}, fmt.Errorf("the work file for %s is missing (%v); run wikory bootstrap plan again", it.Ref, describeConfine(err))
		}
		f.Close()
		rep.Items = append(rep.Items, nextItem{ID: it.ID, Stage: it.Stage, Ref: it.Ref, Epoch: it.Epoch, InputHash: it.InputHash,
			WorkFile: rel, WorkPath: filepath.Join(p.Store.Dir, filepath.FromSlash(rel))})
	}
	return Result{Data: rep, Text: rep.Status + "\n"}, nil
}

// statusLine is the one-line summary the driver mirrors on its task list.
func statusLine(st bootstrap.RunStatus) string {
	var parts []string
	stages := make([]string, 0, len(st.Items))
	for s := range st.Items {
		stages = append(stages, s)
	}
	sort.Strings(stages)
	for _, s := range stages {
		c := st.Items[s]
		total := 0
		for _, n := range c {
			total += n
		}
		parts = append(parts, fmt.Sprintf("%s %d/%d done", s, c["done"], total))
	}
	return fmt.Sprintf("run %s is %s: %s", st.Run.ID, st.Run.State, strings.Join(parts, ", "))
}

type statusReport struct {
	Run          bootstrap.Run             `json:"run"`
	Items        map[string]map[string]int `json:"items"`
	Dispositions map[string]int            `json:"sources"`
	Components   int                       `json:"components"`
	Claims       map[string]int            `json:"claims"`
	Summary      string                    `json:"summary"`
	TokensNote   string                    `json:"tokens_note"`
	// AdmissionsNote says what run.claims_accepted and run.claims_rejected count.
	AdmissionsNote string `json:"admissions_note"`
}

// admissionsNote is the plain meaning of a run's accepted and rejected counts.
const admissionsNote = "claims_accepted and claims_rejected count extract submissions only (claims admitted or rejected when first proposed); verify verdicts are not counted in them"

func bootstrapStatus(ctx context.Context, env *Env, args []string) (Result, error) {
	p, runs, fl, err := openRun(ctx, env, args, map[string]bool{"run": true})
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	id := oneFlag(fl, "run")
	if id == "" {
		latest, err := runs.Latest(ctx)
		if err != nil {
			return Result{}, err
		}
		id = latest.ID
	}
	st, err := runs.Status(ctx, id)
	if err != nil {
		return Result{}, err
	}
	rep := statusReport{Run: st.Run, Items: st.Items, Dispositions: st.Dispositions, Components: st.Components, Claims: st.Claims, Summary: statusLine(st),
		TokensNote: "token counts are asserted by the driver; the binary cannot measure them", AdmissionsNote: admissionsNote}
	text := fmt.Sprintf("%s\nextract admissions: %d accepted, %d rejected (verify verdicts are not counted here)\ntokens (asserted): %d in, %d out\n",
		rep.Summary, st.Run.Accepted, st.Run.Rejected, st.Run.TokensIn, st.Run.TokensOut)
	return Result{Data: rep, Text: text}, nil
}
