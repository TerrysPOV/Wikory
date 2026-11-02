package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/TerrysPOV/Wikory/internal/capture"
	"github.com/TerrysPOV/Wikory/internal/confine"
)

func init() {
	register(Command{Name: "doctor", Summary: "report binary, harness, mode and store health", Run: runDoctor})
}

func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
}

func runDoctor(ctx context.Context, env *Env, args []string) (Result, error) {
	data := map[string]any{"version": env.Version, "os": runtime.GOOS}
	var warn []string
	exe, _ := os.Executable()
	onPath, _ := exec.LookPath("wikory")
	if onPath != "" {
		onPath, _ = filepath.Abs(onPath)
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	data["executable"], data["wikory_on_path"], data["same_binary"] = exe, onPath, sameFile(exe, onPath)
	if onPath == "" {
		warn = append(warn, "wikory is not on PATH; hooks call wikory from PATH, so install it there")
	} else if !sameFile(exe, onPath) {
		warn = append(warn, "the wikory on PATH ("+onPath+") is not this binary; hooks will run that one")
	}
	cv := probeClaudeVersion(ctx)
	caps, mode := modeFor(cv)
	_, known := knownGood[cv]
	data["claude_version"], data["claude_known_good"], data["capabilities"], data["mode"] = cv, known, caps, mode
	data["stage0_limits"] = stage0Limits
	hl := readHookLog(env, time.Now())
	data["hook_errors"] = hl.Recent // the last 24 hours
	if hl.Recent > 0 {
		warn = append(warn, fmt.Sprintf("%d hook error(s) in the last 24 hours: hooks never fail the session, so this is where they show; the kind and time of each are in hook-errors.log in the data directory", hl.Recent))
	}
	if hl.Problem != "" {
		data["hook_log_problem"] = hl.Problem
		warn = append(warn, hl.Problem)
	}
	if runtime.GOOS == "windows" {
		warn = append(warn, "owner-only ACLs for the private store arrive in a later version; it relies on %LOCALAPPDATA% permissions")
	}
	type rootReport struct {
		Path     string `json:"path"`
		Confined bool   `json:"confined"`
	}
	var roots []rootReport
	for _, r := range transcriptRoots(env) {
		ok := false
		if cr, err := confine.Open(r); err == nil {
			ok = true
			cr.Close()
		}
		roots = append(roots, rootReport{Path: r, Confined: ok})
	}
	data["transcript_roots"] = roots // the only directories transcripts are ever read from (plan adjudication Q19)
	data["initialised"], data["registered"], data["trusted"] = false, false, false
	data["uncommitted_wiki_changes"] = []string{}
	data["index_partial"] = nil
	p, err := openProject(ctx, env, false)
	if err == nil {
		defer p.Close()
		data["initialised"], data["registered"], data["trusted"] = true, p.Registered, p.Trusted
		data["repository"] = p.Repo.Top
		if dirty, err := p.Repo.Dirty(ctx, pmDir); err != nil {
			warn = append(warn, "could not read git status, so uncommitted changes under project-memory/ are unknown: "+err.Error())
		} else {
			data["uncommitted_wiki_changes"] = append([]string{}, dirty...)
			if len(dirty) > 0 {
				warn = append(warn, fmt.Sprintf("%d uncommitted change(s) under project-memory/; Wikory never commits, so review and commit them", len(dirty)))
			}
		}
		if p.Store != nil {
			data["store_dir"] = p.Store.Dir
			if servable(p) {
				if st, err := p.refresh(ctx, nil); err != nil {
					warn = append(warn, "index refresh failed: "+err.Error())
				} else {
					data["index_partial"] = st.Partial
					warn = append(warn, st.Notices...) // partial, refused and malformed pages
				}
			}
			warn = append(warn, p.storeCounts(ctx, data)...)
		}
	} else if err != errNotGit && err != errNotInitialised {
		warn = append(warn, "project: "+err.Error())
	}
	var b strings.Builder
	keys := []string{"version", "executable", "wikory_on_path", "claude_version", "mode", "repository", "store_dir", "initialised", "registered", "trusted", "index_partial", "hook_errors", "pending_turns", "captured_turns", "sealed_turns", "incomplete_turns", "incomplete_jobs", "stuck_jobs", "conflicted_jobs"}
	for _, k := range keys {
		if v, ok := data[k]; ok {
			fmt.Fprintf(&b, "%-18s %v\n", k+":", v)
		}
	}
	return Result{Text: b.String(), Notices: warn, Data: data}, nil
}

// storeCounts fills data with what the private store holds and returns a notice for each thing
// that needs attention. A count that cannot be read is a notice, never a silent zero: doctor
// must not look all clear because its own query failed.
func (p *project) storeCounts(ctx context.Context, data map[string]any) []string {
	var notices []string
	count := func(what, query string, args ...any) int {
		var n int
		if err := p.Store.DB.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			notices = append(notices, "doctor could not count "+what+": "+err.Error())
			data[strings.ReplaceAll(what, " ", "_")+"_error"] = err.Error()
			return 0
		}
		return n
	}
	co := p.Checkout.ID
	// pending_turns is every turn of this checkout: none is consolidated in 1a, so a sealed turn
	// is as pending as a captured one. The two states are reported separately as well.
	pending := count("turns", `SELECT count(*) FROM turns WHERE checkout_id = ?`, co)
	captured := count("captured turns", `SELECT count(*) FROM turns WHERE checkout_id = ? AND state = 'captured'`, co)
	sealed := count("sealed turns", `SELECT count(*) FROM turns WHERE checkout_id = ? AND state = 'sealed'`, co)
	incompleteJobs := count("begun or applying jobs", `SELECT count(*) FROM jobs WHERE checkout_id = ? AND state IN ('begun','applying')`, co)
	conflicts := count("conflicted jobs", `SELECT count(*) FROM jobs WHERE checkout_id = ? AND state = 'conflict'`, co)
	data["pending_turns"], data["captured_turns"], data["sealed_turns"] = pending, captured, sealed
	data["incomplete_jobs"], data["conflicted_jobs"] = incompleteJobs, conflicts
	if conflicts > 0 {
		notices = append(notices, "a publish stopped on a conflict; inspect the reported files before editing them")
	}
	stuck, err := p.stuckJobs(ctx, time.Now())
	if err != nil {
		notices = append(notices, "doctor could not count stuck jobs: "+err.Error())
	}
	data["stuck_jobs"] = stuck
	if stuck > 0 {
		notices = append(notices, fmt.Sprintf("%d publish job(s) have been begun or applying for over %v: a process stopped part way. Run wikory recover to finish or abandon them", stuck, stuckJobAge))
	}
	rec := &capture.Recorder{Store: p.Store, CheckoutID: co}
	n, byReason, err := rec.IncompleteSealed(ctx, 200)
	if err != nil {
		notices = append(notices, "doctor could not read every sealed turn: "+err.Error())
	}
	data["incomplete_turns"] = n
	if n > 0 {
		reasons := make([]string, 0, len(byReason))
		for r, c := range byReason {
			reasons = append(reasons, fmt.Sprintf("%s %d", r, c))
		}
		sort.Strings(reasons)
		data["incomplete_turn_reasons"] = byReason
		notices = append(notices, fmt.Sprintf("%d of the newest sealed turns were sealed incomplete (%s): their transcript could not be fully read, so files and commands are missing from the record", n, strings.Join(reasons, ", ")))
	}
	return notices
}
