package app

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/publish"
)

// TestMain lets this test binary stand in for a killable finish: the kill test runs it again as
// a child that dies at a named step.
func TestMain(m *testing.M) {
	if os.Getenv("WIKORY_TEST_ROLE") == "bootstrap-finish-helper" {
		os.Exit(finishHelper())
	}
	os.Exit(m.Run())
}

// dieAt writes the step it died at to the marker file the parent named, then kills the process.
// The marker is written immediately before the kill, so the parent can tell a helper that was
// killed at the named step from one that died, or never started, for any other reason. If the
// marker cannot be written the helper exits with a code that is not a kill (78), so the parent
// fails instead of trusting a kill it cannot confirm.
func dieAt(step string) {
	if err := os.WriteFile(os.Getenv("WIKORY_KILL_MARKER"), []byte(step), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "kill marker:", err)
		os.Exit(78)
	}
	killSelf()
}

// finishHelper runs `wikory bootstrap finish` on the fixture the parent built and kills itself
// at the step in WIKORY_KILL_AT, with no deferred call run. A step of the form "confine:S:N"
// dies at the Nth (from zero) time confine's replace reports step S, which is how a kill lands
// in the middle of replacing a page that already exists.
func finishHelper() int {
	at := os.Getenv("WIKORY_KILL_AT")
	hook := func(step string) {
		if step == at {
			dieAt(at)
		}
	}
	if parts := strings.Split(at, ":"); len(parts) == 3 && parts[0] == "confine" {
		want, err := strconv.Atoi(parts[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, "kill step", at, "has no valid count:", err)
			return 79
		}
		seen := 0
		confine.SetStepHook(func(s string) {
			if s != parts[1] {
				return
			}
			if seen == want {
				dieAt(at)
			}
			seen++
		})
	}
	publish.SetKillHook(hook)
	killPoint = hook
	maxOpsPerJob = 2 // several jobs from a small fixture, so there are boundaries between them
	data := os.Getenv("WIKORY_TEST_DATA")
	env := &Env{IO: IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}, Version: "test", Cwd: os.Getenv("WIKORY_TEST_REPO"),
		Getenv: func(k string) string {
			if k == "WIKORY_DATA_DIR" {
				return data
			}
			return os.Getenv(k)
		}}
	RunEnv([]string{"bootstrap", "finish", "--run", os.Getenv("WIKORY_TEST_RUN")}, env)
	return 0 // reached only if the step was never hit
}

// finishBoundaries are the places a finish can die. "checked" and "planned" are bootstrap's,
// before anything is published (the run is already finishing at "planned"). The next ten are the
// publisher's own journal steps in the first job (names from internal/publish/kill_test.go); the
// rest are bootstrap's. rerun says whether finishing again is still allowed: after "marked-done"
// the run is already done, so the check is that everything is complete and finish refuses.
var finishBoundaries = []struct {
	step  string
	rerun bool
}{
	{"checked", true}, {"planned", true}, {"pending-0", true}, {"locked", true}, {"staged", true}, {"manifest", true},
	{"applied-0", true}, {"marked-0", true}, {"dbmarked-0", true}, {"applied-1", true},
	{"appended", true}, {"logged", true}, {"committed", true},
	{"after-job-0", true}, {"pending-1", true}, {"after-job-1", true}, {"before-record", true}, {"recorded", true}, {"marked-done", false},
}

type finishPages struct {
	Pages []struct {
		Path    string `json:"path"`
		Outcome string `json:"outcome"`
	} `json:"pages"`
}

func killFinishAt(t *testing.T, dir string, c *cli, run, step string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "kill-marker") // outside the repository, so it cannot dirty it
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), "WIKORY_TEST_ROLE=bootstrap-finish-helper", "WIKORY_KILL_AT="+step,
		"WIKORY_KILL_MARKER="+marker, "WIKORY_TEST_DATA="+c.dataDir, "WIKORY_TEST_REPO="+dir, "WIKORY_TEST_RUN="+run)
	out, err := cmd.CombinedOutput()
	// Only a real kill counts: a helper that never started, exited by itself or died by another
	// signal would leave a finish that was never interrupted, and the rerun would trivially pass.
	if !diedByTheKill(err) {
		t.Fatalf("step %s: the helper was not killed (%v): %s", step, err, out)
	}
	got, rerr := os.ReadFile(marker)
	if rerr != nil {
		t.Fatalf("step %s: the helper died but left no kill marker, so it was not killed at that step (%v): %s", step, rerr, out)
	}
	if string(got) != step {
		t.Fatalf("step %s: the helper died at %q, not at the named step: %s", step, got, out)
	}
}

// assertFinishConverges is what must hold once a finish of run was killed at step: a rerun
// completes without taking a page this very bootstrap wrote for a person's edit (or, when the
// kill came after the run was marked done, finish refuses), the repository holds exactly the pages
// in want and no temporary or backup file, every page is recorded with the hash of what is on
// disk, the run is done, and a further bootstrap sees every page as an earlier output of Wikory.
func assertFinishConverges(t *testing.T, dir string, c *cli, run, step string, rerun bool, want []string) {
	t.Helper()
	if rerun {
		e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run)
		if code != ExitOK || !e.OK {
			t.Fatalf("finish after a kill at %s: %d %+v", step, code, e)
		}
		for _, p := range decodeData[finishPages](t, e).Pages {
			if p.Outcome == "conflict" {
				t.Fatalf("%s: a page this very bootstrap wrote was taken for a person's edit", p.Path)
			}
		}
	} else if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code == ExitOK || e.OK {
		t.Fatal("a run that was marked done before the kill cannot be finished again")
	}
	// Exactly the expected pages: none lost, none duplicated, no temporary or backup file.
	base := filepath.Join(dir, "project-memory", "wiki", "bootstrap")
	ents, err := os.ReadDir(base)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, en := range ents {
		if strings.HasPrefix(en.Name(), confine.TempPrefix) || strings.HasPrefix(en.Name(), confine.BackupPrefix) {
			t.Fatalf("temporary or backup file left behind: %s", en.Name())
		}
		got = append(got, en.Name())
	}
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Fatalf("pages after recovery = %v, want %v", got, want)
	}
	// Every page is recorded with the hash of what is on disk, or the next bootstrap would call
	// it a person's edit.
	db := openTestDB(t, c)
	defer db.Close()
	for _, name := range got {
		rel := "project-memory/wiki/bootstrap/" + name
		data, err := os.ReadFile(filepath.Join(base, name))
		if err != nil {
			t.Fatal(err)
		}
		var h string
		if err := db.QueryRow(`SELECT hash FROM bs_pages WHERE path=?`, rel).Scan(&h); err != nil || h != publish.HashContent(data) {
			t.Fatalf("%s: the recorded hash must match the file on disk: %q %v", rel, h, err)
		}
	}
	st, _, _ := runBootstrapJSON(t, c, dir, "status", "--run", run)
	if decodeData[struct {
		Run struct {
			State string `json:"state"`
		} `json:"run"`
	}](t, st).Run.State != "done" {
		t.Fatalf("the run should be done: %+v", st)
	}
	// A further bootstrap sees every page as its own earlier output.
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	next := decodeData[planData](t, pe).RunID
	forceState(t, c, next, "running")
	forceItemsClosed(t, c, next)
	e2, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", next)
	if code != ExitOK {
		t.Fatalf("second bootstrap: %d %+v", code, e2)
	}
	for _, p := range decodeData[finishPages](t, e2).Pages {
		if p.Outcome == "conflict" {
			t.Fatalf("%s: the second bootstrap took the first one's page for a person's edit", p.Path)
		}
	}
}

// pageNames lists the file names under the baseline directory, sorted.
func pageNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(dir, "project-memory", "wiki", "bootstrap"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, en := range ents {
		names = append(names, en.Name())
	}
	sort.Strings(names)
	return names
}

func TestFinishSurvivesAProcessKillAtEveryBoundary(t *testing.T) {
	// A clean finish in a fixture of its own says which pages to expect.
	cdir, cc, crun := finishedRunInputs(t)
	forceItemsClosed(t, cc, crun)
	ce, code, _ := runBootstrapJSON(t, cc, cdir, "finish", "--run", crun)
	if code != ExitOK {
		t.Fatalf("control finish: %d %+v", code, ce)
	}
	var want []string
	for _, p := range decodeData[finishPages](t, ce).Pages {
		want = append(want, filepath.Base(p.Path))
	}
	sort.Strings(want)
	if len(want) < 6 {
		t.Fatalf("the fixture should publish at least six pages so that three jobs of two exist: %v", want)
	}
	for _, b := range finishBoundaries {
		t.Run(b.step, func(t *testing.T) {
			dir, c, run := finishedRunInputs(t)
			forceItemsClosed(t, c, run)
			killFinishAt(t, dir, c, run, b.step)
			assertFinishConverges(t, dir, c, run, b.step, b.rerun, want)
		})
	}
}

// replaceBoundaries are kills inside confine's move-aside replace, which only happens when the
// page already exists. The fixture's second bootstrap replaces two pages, so N is 0 or 1; the Nth
// replace of the child process dies after its move-aside, after the
// comparison, or after the install (when the live page, a backup and the staged temporary all
// still exist).
var replaceBoundaries = []string{
	"confine:moved:0", "confine:compared:0", "confine:installed:0",
	"confine:moved:1", "confine:installed:1",
}

// secondBootstrapInputs finishes a first bootstrap, then plans a second one whose items are all
// closed with no claims, so that its render differs from the first and finishing it replaces
// pages the first one wrote.
func secondBootstrapInputs(t *testing.T) (string, *cli, string) {
	t.Helper()
	dir, c, run := finishedRunInputs(t)
	forceItemsClosed(t, c, run)
	if e, code, _ := runBootstrapJSON(t, c, dir, "finish", "--run", run); code != ExitOK {
		t.Fatalf("first bootstrap: %d %+v", code, e)
	}
	pe, _, _ := runBootstrapJSON(t, c, dir, "plan")
	run2 := decodeData[planData](t, pe).RunID
	forceState(t, c, run2, "running")
	forceItemsClosed(t, c, run2)
	return dir, c, run2
}

func TestASecondBootstrapFinishSurvivesAKillInsideAReplaceOfAnEarlierPage(t *testing.T) {
	cdir, cc, crun := secondBootstrapInputs(t)
	first := pageNames(t, cdir)
	if e, code, _ := runBootstrapJSON(t, cc, cdir, "finish", "--run", crun); code != ExitOK {
		t.Fatalf("control second finish: %d %+v", code, e)
	}
	want := pageNames(t, cdir)
	if len(want) < len(first) {
		t.Fatalf("a second bootstrap does not delete pages: %v then %v", first, want)
	}
	for _, step := range replaceBoundaries {
		t.Run(step, func(t *testing.T) {
			dir, c, run := secondBootstrapInputs(t)
			killFinishAt(t, dir, c, run, step)
			assertFinishConverges(t, dir, c, run, step, true, want)
		})
	}
}
