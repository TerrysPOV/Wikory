package bootstrap

import (
	"context"
	"strings"
	"testing"
)

// next refused for a run that cannot hand out work says what to do in that state. Only a run a
// confirm can still start is pointed at confirm; a finishing, done or abandoned run never is.
func TestNextRefusalSaysWhatToDoInEachState(t *testing.T) {
	ctx := context.Background()
	want := map[string]string{
		StatePlanned:   "not been confirmed",
		StateAwaiting:  "confirm the full run",
		StateFinishing: "wikory bootstrap finish",
		StateDone:      "plan again",
		StateAbandoned: "plan again",
	}
	for state, phrase := range want {
		r, _, _, _ := newTestRuns(t)
		run := mustRun(t, r, 1, 2)
		if _, err := r.DB.Exec(`UPDATE bs_runs SET state=? WHERE run_id=?`, state, run.ID); err != nil {
			t.Fatal(err)
		}
		_, err := r.Next(ctx, run.ID, 8)
		if err == nil || !strings.Contains(err.Error(), phrase) {
			t.Errorf("%s: next refusal %v, want it to say %q", state, err, phrase)
		}
		if state == StateFinishing || state == StateDone || state == StateAbandoned {
			if strings.Contains(err.Error(), "confirm") {
				t.Errorf("%s: the refusal must not suggest confirm: %v", state, err)
			}
		}
	}
}
