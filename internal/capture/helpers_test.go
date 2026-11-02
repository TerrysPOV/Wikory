package capture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/decode"
	"github.com/TerrysPOV/Wikory/internal/gitx"
	"github.com/TerrysPOV/Wikory/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type rig struct {
	rec        *Recorder
	clk        *clock
	transcript string
	repoDir    string
	dataDir    string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ctx := context.Background()
	repoDir, _ := filepath.EvalSymlinks(t.TempDir())
	for _, args := range [][]string{{"-c", "init.defaultBranch=main", "init", "-q"}, {"commit", "--allow-empty", "-qm", "init"}} {
		c := exec.Command("git", append([]string{"-C", repoDir}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@invalid")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	repo, _ := gitx.Discover(ctx, repoDir)
	repoRoot, _ := confine.Open(repoDir)
	dataDir := t.TempDir()
	st, err := store.Resolve(ctx, dataDir, store.Identity{ProjectID: "wkp_P", CommonDir: repo.CommonDir, Worktree: repo.Top}, true)
	if err != nil {
		t.Fatal(err)
	}
	co, _ := st.Register(ctx, store.Identity{ProjectID: "wkp_P", CommonDir: repo.CommonDir, Worktree: repo.Top})
	tdir, _ := filepath.EvalSymlinks(t.TempDir())
	tpath := filepath.Join(tdir, "session.jsonl")
	_ = os.WriteFile(tpath, nil, 0o600)
	clk := &clock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	t.Cleanup(func() { st.Close(); repoRoot.Close() })
	return &rig{rec: &Recorder{Store: st, CheckoutID: co.ID, Repo: repo, RepoRoot: repoRoot,
		TranscriptRoots: []string{tdir}, Now: clk.now, SealTimeout: 10 * time.Minute, StaleMarkerAfter: 6 * time.Hour},
		clk: clk, transcript: tpath, repoDir: repoDir, dataDir: dataDir}
}

// second opens another, independent store handle on the same store, as a second hook process would.
func (r *rig) second(t *testing.T) *Recorder {
	t.Helper()
	st, err := store.Resolve(context.Background(), r.dataDir, store.Identity{ProjectID: "wkp_P", CommonDir: r.rec.Repo.CommonDir, Worktree: r.rec.Repo.Top}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	other := *r.rec
	other.Store = st
	return &other
}

func (r *rig) appendTranscript(t *testing.T, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(r.transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *rig) ev(kind decode.Kind, prompt, promptID, final string) decode.Event {
	return decode.Event{Kind: kind, Harness: "claude-code", SessionID: "sess-1", PromptID: promptID,
		TranscriptPath: r.transcript, Cwd: r.repoDir, Prompt: prompt, LastAssistantMessage: final}
}

func (r *rig) versions(t *testing.T, turnID string) []string {
	t.Helper()
	ents, _ := os.ReadDir(filepath.Join(r.rec.Store.Dir, "turns", r.rec.CheckoutID, turnID))
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "v") {
			names = append(names, e.Name())
		}
	}
	return names
}

func (r *rig) readVersion(t *testing.T, turnID, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.rec.Store.Dir, "turns", r.rec.CheckoutID, turnID, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *rig) row(t *testing.T, turnID string) (state string, version int, stub bool, outcome string) {
	t.Helper()
	var s int
	if err := r.rec.Store.DB.QueryRow(`SELECT state, version, stub, outcome FROM turns WHERE turn_id = ?`, turnID).Scan(&state, &version, &s, &outcome); err != nil {
		t.Fatal(err)
	}
	return state, version, s == 1, outcome
}

// farDeadline is a Seal deadline an hour past the recorder's own clock, so a slow machine can
// never run a test out of time: Seal judges the deadline against Recorder.Now, which the test moves.
func (r *rig) farDeadline() time.Time { return r.rec.Now().Add(time.Hour) }
