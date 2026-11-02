package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/TerrysPOV/Wikory/internal/confine"
)

type recoverOut struct {
	OK   bool `json:"ok"`
	Data struct {
		Completed []string `json:"completed"`
		Conflicts []string `json:"conflicts"`
		Refused   []string `json:"refused"`
		Abandoned []string `json:"abandoned"`
	} `json:"data"`
	Notices []string `json:"notices"`
}

func runRecoverJSON(t *testing.T, c *cli, dir string) (recoverOut, string, int) {
	t.Helper()
	out, errOut, code := c.run(dir, "--json", "recover")
	var r recoverOut
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("recover output is not the envelope: %v\n%q (stderr %q)", err, out, errOut)
	}
	return r, errOut, code
}

// jobStates returns job id -> state for every publish job of the store behind dir.
func jobStates(t *testing.T, dir string, c *cli) map[string]string {
	t.Helper()
	env := testEnv(c, dir)
	p, err := openProject(context.Background(), env, false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	rows, err := p.Store.DB.Query(`SELECT job_id, state FROM jobs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			t.Fatal(err)
		}
		got[id] = st
	}
	return got
}

// A stamp that stops after it moved the live page aside leaves a job applying, with its
// manifest. `wikory recover` must finish it: the page ends up stamped, the report names the job
// as completed and the exit status is 0. The stop is simulated in process (Goexit from the
// confine step hook, which skips the rest of the stamp exactly as a kill would, apart from
// deferred cleanup), because the publish package's kill helper is a separate test binary.
func TestRecoverFinishesAStampThatStoppedMidApply(t *testing.T) {
	dir, c := stampRepo(t, handPage)
	page := filepath.Join(dir, "project-memory", "wiki", "q.md")

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		confine.SetStepHook(func(step string) {
			if step == "moved" {
				runtime.Goexit()
			}
		})
		c.run(dir, "page", "stamp", "project-memory/wiki/q.md")
	}()
	<-stopped
	confine.SetStepHook(nil)

	var stuck string
	for id, st := range jobStates(t, dir, c) {
		if st != "done" {
			stuck = id
		}
	}
	if stuck == "" {
		t.Fatal("test premise broken: the stamp finished, so there is no interrupted job")
	}
	if _, err := os.Stat(page); err == nil {
		t.Fatal("test premise broken: the live page is still in place, so the stamp did not stop after moving it aside")
	}

	r, errOut, code := runRecoverJSON(t, c, dir)
	if code != ExitOK || !r.OK || len(r.Data.Completed) != 1 || r.Data.Completed[0] != stuck ||
		len(r.Data.Conflicts)+len(r.Data.Refused)+len(r.Data.Abandoned) != 0 {
		t.Fatalf("code=%d recover=%+v stderr=%s", code, r, errOut)
	}
	if got := readFile(t, page); !strings.Contains(got, "c=") || !strings.Contains(got, "status=verified") {
		t.Fatalf("recovery did not finish the stamp: %q", got)
	}
	if st := jobStates(t, dir, c)[stuck]; st != "done" {
		t.Fatalf("the recovered job is %q, want done", st)
	}
	// Text form, and a second run finds nothing left.
	out, _, code := c.run(dir, "recover")
	if code != ExitOK || !strings.Contains(out, "recovered: 0 completed, 0 conflicts, 0 refused, 0 abandoned") {
		t.Fatalf("second recover: code=%d out=%q", code, out)
	}
}

func testEnv(c *cli, dir string) *Env {
	return &Env{IO: IO{In: strings.NewReader(""), Out: &strings.Builder{}, Err: &strings.Builder{}}, Version: "test", Cwd: dir, Getenv: c.getenv()}
}

// beginJobs opens a publish job per age given (how long ago it began) and never writes a
// manifest for it, as a process that stopped right after Begin leaves it.
func beginJobs(t *testing.T, dir string, c *cli, ages ...time.Duration) []string {
	t.Helper()
	p, err := openProject(context.Background(), testEnv(c, dir), false)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var ids []string
	for _, age := range ages {
		j, err := p.publisher().Begin(context.Background(), "test")
		if err != nil {
			t.Fatal(err)
		}
		at := time.Now().Add(-age).UTC().Format(time.RFC3339Nano)
		if _, err := p.Store.DB.Exec(`UPDATE jobs SET created_at = ? WHERE job_id = ?`, at, j.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	return ids
}

// A job begun and never given a manifest, older than the abandon age (ten minutes), is
// abandoned by recover, in the text form and in the JSON form: the process stopped before it
// wrote anything. A job begun a minute ago is still live and is left alone.
func TestRecoverAbandonsAnOldJobWithoutAManifestAndKeepsAFreshOne(t *testing.T) {
	dir, c := initRepoWithPage(t)
	ids := beginJobs(t, dir, c, time.Hour, time.Minute)
	oldText, fresh := ids[0], ids[1]

	out, errOut, code := c.run(dir, "recover")
	if code != ExitOK || !strings.Contains(out, "recovered: 0 completed, 0 conflicts, 0 refused, 1 abandoned") {
		t.Fatalf("text form: code=%d out=%q stderr=%q", code, out, errOut)
	}
	oldJSON := beginJobs(t, dir, c, 2*time.Hour)[0]
	r, errOut, code := runRecoverJSON(t, c, dir)
	if code != ExitOK || !r.OK || len(r.Data.Abandoned) != 1 || r.Data.Abandoned[0] != oldJSON || len(r.Data.Completed) != 0 {
		t.Fatalf("JSON form: code=%d recover=%+v stderr=%s", code, r, errOut)
	}
	states := jobStates(t, dir, c)
	if states[oldText] != "abandoned" || states[oldJSON] != "abandoned" || states[fresh] != "begun" {
		t.Fatalf("job states %v: want both old jobs abandoned and the fresh one begun", states)
	}
}

// --limit reaches the retrieval: five pages match (each as a page hit and a claim hit), and the
// number of hits returned follows the flag. Without it the default of ten applies.
func TestSearchLimitIsPlumbedThrough(t *testing.T) {
	dir, c := initRepoWithPage(t)
	for i := 0; i < 5; i++ {
		page := fmt.Sprintf("---\ntype: decision\ntitle: Gadget %d\n---\n# Gadget %d\n<!-- claim kind=intent status=unverified -->\nThe gadget %d policy is documented here.\n", i, i, i)
		if err := os.WriteFile(filepath.Join(dir, "project-memory", "wiki", fmt.Sprintf("g%d.md", i)), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hits := func(args ...string) int {
		t.Helper()
		out, errOut, code := c.run(dir, append([]string{"--json", "search"}, args...)...)
		var e struct {
			Data struct {
				Hits []json.RawMessage `json:"hits"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &e); code != ExitOK || err != nil {
			t.Fatalf("code=%d err=%v out=%q stderr=%q", code, err, out, errOut)
		}
		return len(e.Data.Hits)
	}
	if n := hits("gadget"); n != 10 {
		t.Fatalf("default limit: %d hits, want 10", n)
	}
	for _, want := range []int{1, 2, 3, 7} {
		if n := hits("--limit", fmt.Sprint(want), "gadget"); n != want {
			t.Errorf("--limit %d returned %d hits", want, n)
		}
	}
}
