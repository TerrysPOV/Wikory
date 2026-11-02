package app

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// stubClaudeVersion makes the version probe answer v, clears the process-wide mode cache, and
// returns a counter of how many times the probe ran.
func stubClaudeVersion(t *testing.T, v string) *atomic.Int32 {
	t.Helper()
	calls := new(atomic.Int32)
	probeClaudeVersion = func(context.Context) string { calls.Add(1); return v }
	resetModeCache()
	t.Cleanup(func() { probeClaudeVersion = defaultProbeClaudeVersion; resetModeCache() })
	return calls
}

func (h *hookRig) getenv() func(string) string {
	return func(k string) string {
		if k == "WIKORY_DATA_DIR" {
			return h.c.dataDir
		}
		if v, ok := h.c.extra[k]; ok {
			return v
		}
		return os.Getenv(k)
	}
}

// hookEnv builds the environment a hook runs in, with stdout going to out.
func (h *hookRig) hookEnv(stdin string, out io.Writer) *Env {
	return &Env{IO: IO{In: strings.NewReader(stdin), Out: out, Err: io.Discard}, Version: "test", Cwd: h.dir, Getenv: h.getenv()}
}

// runHookTimed runs one hook with stdin from stdin, reports its stdout, exit code and how long
// it took, and fails if it has not returned within limit.
func runHookTimed(t *testing.T, h *hookRig, arg, stdin string, limit time.Duration) (string, time.Duration, int) {
	t.Helper()
	var out bytes.Buffer
	e := h.hookEnv(stdin, &out)
	done := make(chan int, 1)
	start := time.Now()
	go func() { done <- RunEnv([]string{"hook", arg}, e) }()
	select {
	case code := <-done:
		return out.String(), time.Since(start), code
	case <-time.After(limit):
		t.Fatalf("hook %s did not return within %v", arg, limit)
		return "", 0, 0
	}
}

func (h *hookRig) logText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(h.c.dataDir, "hook-errors.log"))
	if err != nil {
		return ""
	}
	return string(b)
}

// logKinds returns the kind (second field) of each line of the hook error log.
func (h *hookRig) logKinds(t *testing.T) []string {
	t.Helper()
	var kinds []string
	for _, l := range strings.Split(strings.TrimSpace(h.logText(t)), "\n") {
		if f := strings.Fields(l); len(f) == 2 {
			kinds = append(kinds, f[1])
		}
	}
	return kinds
}

func has(kinds []string, kind string) bool {
	for _, k := range kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (h *hookRig) storeDir(t *testing.T) string {
	t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(h.c.dataDir, "stores", "*"))
	if len(dirs) != 1 {
		t.Fatalf("want one store, found %v", dirs)
	}
	return dirs[0]
}

func (h *hookRig) storeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(h.storeDir(t), "wikory.db"))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func (h *hookRig) checkoutID(t *testing.T) string {
	t.Helper()
	var id string
	if err := h.storeDB(t).QueryRow(`SELECT id FROM checkouts LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// openRigProject opens the rig's project the way a command does.
func openRigProject(t *testing.T, h *hookRig) (*project, *Env) {
	t.Helper()
	e := h.hookEnv("", io.Discard)
	p, err := openProject(context.Background(), e, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p, e
}

type doctorEnvelope struct {
	Data    map[string]any `json:"data"`
	Notices []string       `json:"notices"`
}

func doctorFull(t *testing.T, c *cli, dir string) doctorEnvelope {
	t.Helper()
	out, errOut, code := c.run(dir, "--json", "doctor")
	if code != ExitOK {
		t.Fatalf("doctor exit %d: %s", code, errOut)
	}
	var e doctorEnvelope
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
