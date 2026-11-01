package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/store"
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

// countHookErrors counts the lines of the hook error log (kind and time only, never content).
// It reads at most 1 MiB of a regular file, so a hostile log cannot stall doctor.
func countHookErrors(env *Env) int {
	dir, err := store.DataDir(env.Getenv)
	if err != nil {
		return 0
	}
	path := filepath.Join(dir, "hook-errors.log")
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	return strings.Count(string(b), "\n")
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
	data["hook_errors"] = countHookErrors(env)
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
	data["transcript_roots"] = roots // the only directories transcripts are ever read from (spec question Q19)
	data["initialised"], data["registered"], data["trusted"] = false, false, false
	data["uncommitted_wiki_changes"] = []string{}
	data["index_partial"] = nil
	p, err := openProject(ctx, env, false)
	if err == nil {
		defer p.Close()
		data["initialised"], data["registered"], data["trusted"] = true, p.Registered, p.Trusted
		data["repository"] = p.Repo.Top
		if dirty, err := p.Repo.Dirty(ctx, pmDir); err == nil {
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
					if st.Partial {
						warn = append(warn, "the wiki index is partial: a size limit kept some pages out of search and the briefing")
					}
				}
			}
			var pending, incomplete, conflicts int
			_ = p.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM turns WHERE checkout_id = ?`, p.Checkout.ID).Scan(&pending)
			_ = p.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE checkout_id = ? AND state IN ('begun','applying')`, p.Checkout.ID).Scan(&incomplete)
			_ = p.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM jobs WHERE checkout_id = ? AND state = 'conflict'`, p.Checkout.ID).Scan(&conflicts)
			data["pending_turns"], data["incomplete_jobs"], data["conflicted_jobs"] = pending, incomplete, conflicts
			if conflicts > 0 {
				warn = append(warn, "a publish stopped on a conflict; inspect the reported files before editing them")
			}
		}
	} else if err != errNotGit && err != errNotInitialised {
		warn = append(warn, "project: "+err.Error())
	}
	var b strings.Builder
	keys := []string{"version", "executable", "wikory_on_path", "claude_version", "mode", "repository", "store_dir", "initialised", "registered", "trusted", "index_partial", "hook_errors", "pending_turns", "incomplete_jobs", "conflicted_jobs"}
	for _, k := range keys {
		if v, ok := data[k]; ok {
			fmt.Fprintf(&b, "%-18s %v\n", k+":", v)
		}
	}
	return Result{Text: b.String(), Notices: warn, Data: data}, nil
}
