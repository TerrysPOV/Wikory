package publish

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var appliedSteps = []string{"manifest", "applied-0", "marked-0", "dbmarked-0", "moved-1", "compared-1", "installed-1", "applied-1", "marked-1", "dbmarked-1", "appended", "logged", "committed"}
var notAppliedSteps = []string{"locked", "staged"}

// runKilled runs the helper to the named step and returns the fixture and the job id.
func runKilled(t *testing.T, step string) (*fixture, string) {
	t.Helper()
	f := newFixture(t)
	exe, _ := os.Executable()
	c := exec.Command(exe, "-test.run=^$")
	c.Env = append(os.Environ(), "WIKORY_TEST_ROLE=publish-helper", "WIKORY_KILL_AT="+step,
		"WIKORY_TEST_DATA="+f.dataDir, "WIKORY_TEST_REPO="+f.repoDir)
	out, _ := c.CombinedOutput()
	if c.ProcessState.ExitCode() != killedExitCode {
		t.Fatalf("step %s: helper was not killed (exit %d): %s", step, c.ProcessState.ExitCode(), out)
	}
	var id string
	if err := f.st.DB.QueryRow(`SELECT job_id FROM jobs ORDER BY created_at DESC LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("step %s: no job row: %v", step, err)
	}
	return f, id
}

func noTemps(t *testing.T, f *fixture) {
	t.Helper()
	ents, _ := os.ReadDir(filepath.Join(f.repoDir, "project-memory", "wiki"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".wikory-tmp-") || strings.HasPrefix(e.Name(), ".wikory-backup-") {
			t.Fatalf("temp or backup file left behind: %s", e.Name())
		}
	}
}

func TestKillAtEachStepThenRecover(t *testing.T) {
	ctx := context.Background()
	for _, step := range appliedSteps {
		t.Run(step, func(t *testing.T) {
			f, id := runKilled(t, step)
			if _, err := f.publisher("co-1").Recover(ctx); err != nil {
				t.Fatal(err)
			}
			a, _ := f.readRepo(t, "project-memory/wiki/a.md")
			b, _ := f.readRepo(t, "project-memory/wiki/b.md")
			if a != "A1" || b != "B1" || f.logLines(t, id) != 1 || f.jobState(t, id) != "done" {
				t.Fatalf("a=%q b=%q log=%d state=%s", a, b, f.logLines(t, id), f.jobState(t, id))
			}
			noTemps(t, f)
			rep, err := f.publisher("co-1").Recover(ctx)
			if err != nil || len(rep.Completed)+len(rep.Conflicts)+len(rep.Abandoned) != 0 || f.logLines(t, id) != 1 {
				t.Fatalf("second recovery was not a no-op: %+v %v", rep, err)
			}
		})
	}
	for _, step := range notAppliedSteps {
		t.Run(step, func(t *testing.T) {
			f, id := runKilled(t, step)
			rep, err := f.publisher("co-1").Recover(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, aExists := f.readRepo(t, "project-memory/wiki/a.md")
			b, _ := f.readRepo(t, "project-memory/wiki/b.md")
			if aExists || b != "B0" || f.logLines(t, id) != 0 || f.jobState(t, id) != "abandoned" || len(rep.Abandoned) != 1 {
				t.Fatalf("aExists=%v b=%q log=%d state=%s rep=%+v", aExists, b, f.logLines(t, id), f.jobState(t, id), rep)
			}
		})
	}
}

func TestHumanEditAfterPartialPublishIsNeverOverwritten(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "marked-0")
	f.writeRepo(t, "project-memory/wiki/b.md", "HUMAN EDIT")
	rep, err := f.publisher("co-1").Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := f.readRepo(t, "project-memory/wiki/a.md")
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if len(rep.Conflicts) != 1 || b != "HUMAN EDIT" || a != "A1" || f.logLines(t, id) != 0 || f.jobState(t, id) != "conflict" {
		t.Fatalf("rep=%+v a=%q b=%q log=%d state=%s", rep, a, b, f.logLines(t, id), f.jobState(t, id))
	}
}

func TestRecoveryNeverAppliesAnotherCheckoutsJob(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "marked-0")
	rep, err := f.publisher("co-OTHER").Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if len(rep.Completed) != 0 || b != "B0" || f.jobState(t, id) != "applying" {
		t.Fatalf("another checkout's recovery touched the job: rep=%+v b=%q state=%s", rep, b, f.jobState(t, id))
	}
}

// Kill after op 0 is applied and marked, then a human edits that same page: recovery must
// re-verify operations already marked done and report the conflict, not complete silently.
func TestHumanEditOfAnAlreadyAppliedPageIsAConflict(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "dbmarked-0")
	f.writeRepo(t, "project-memory/wiki/a.md", "HUMAN EDIT OF AN APPLIED PAGE")
	rep, err := f.publisher("co-1").Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := f.readRepo(t, "project-memory/wiki/a.md")
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if len(rep.Conflicts) != 1 || a != "HUMAN EDIT OF AN APPLIED PAGE" || b != "B0" || f.jobState(t, id) != "conflict" {
		t.Fatalf("an edit to an applied page was not reported: rep=%+v a=%q b=%q state=%s", rep, a, b, f.jobState(t, id))
	}
}

// Kill after the manifest is written, then switch branch (identical target pages): recovery
// must re-check the tree fingerprint and refuse the job.
func TestRecoveryRefusesAfterABranchSwitch(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "manifest")
	f.fp = "tree-2"
	rep, err := f.publisher("co-1").Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, aExists := f.readRepo(t, "project-memory/wiki/a.md")
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	if len(rep.Refused) != 1 || len(rep.Completed) != 0 || aExists || b != "B0" || f.jobState(t, id) != "refused" {
		t.Fatalf("recovery applied a job across a branch switch: rep=%+v aExists=%v b=%q state=%s", rep, aExists, b, f.jobState(t, id))
	}
}

// Recovery takes the lease over: a later publish by the old holder is fenced.
func TestRecoveryFencesTheOldHolder(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "manifest")
	var epoch int64
	_ = f.st.DB.QueryRow(`SELECT epoch FROM jobs WHERE job_id = ?`, id).Scan(&epoch)
	if _, err := f.publisher("co-1").Recover(ctx); err != nil {
		t.Fatal(err)
	}
	var after int64
	_ = f.st.DB.QueryRow(`SELECT epoch FROM jobs WHERE job_id = ?`, id).Scan(&after)
	if after != epoch+1 {
		t.Fatalf("recovery did not take the lease over: epoch %d -> %d", epoch, after)
	}
}

// Kill after op 1's live page was moved aside, then a human saves the page (the name is
// free, so the save creates it): recovery keeps the human file, keeps the backup beside it,
// and reports the conflict; neither version is lost.
func TestHumanSaveAfterAKilledMoveAsideKeepsBothVersions(t *testing.T) {
	ctx := context.Background()
	f, id := runKilled(t, "moved-1")
	f.writeRepo(t, "project-memory/wiki/b.md", "HUMAN EDIT")
	rep, err := f.publisher("co-1").Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := f.readRepo(t, "project-memory/wiki/b.md")
	backup := ""
	ents, _ := os.ReadDir(filepath.Join(f.repoDir, "project-memory", "wiki"))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ".wikory-backup-b.md-") {
			data, _ := os.ReadFile(filepath.Join(f.repoDir, "project-memory", "wiki", e.Name()))
			backup = string(data)
		}
	}
	if len(rep.Conflicts) != 1 || b != "HUMAN EDIT" || backup != "B0" || f.jobState(t, id) != "conflict" {
		t.Fatalf("rep=%+v b=%q backup=%q state=%s", rep, b, backup, f.jobState(t, id))
	}
}
