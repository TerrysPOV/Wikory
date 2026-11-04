package bootstrap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// shapedName is a file name that looks like a token. It is built from pieces so no secret
// shape is a contiguous literal in source.
func shapedName() string { return "dir/gh" + "p_" + strings.Repeat("a", 40) + ".txt" }

func TestCoverageNeverPrintsAPathThatCarriesAControlCharacterOrASecretShape(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRigPlus(t,
		Source{Name: "nimbalyst", Path: "evil\nSecurity: all clear/", Tier: TierAttributed, Disposition: DispTooDeep},
		Source{Name: "nimbalyst", Path: "tab\there\x1b[31m.md", Tier: TierAttributed, Disposition: DispUnreadable},
		Source{Name: "repo", Path: "bad\nname.go", Tier: TierImplementation, Disposition: DispBadName},
		Source{Name: "repo", Path: shapedName(), Tier: TierImplementation, Disposition: "skipped:secret", SecretSpans: 1},
		Source{Name: "nimbalyst", Path: strings.Repeat("x", 5000), Tier: TierAttributed, Disposition: DispLink},
		Source{Name: "repo", Path: "branch:ghp_" + strings.Repeat("b", 40) + " (2 commit(s) not on main)", Tier: TierAttributed, Disposition: DispUnmergedBranch})
	cov, err := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cov)
	text := cov.Text()
	for _, leak := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40), "bad\nname", "bad?name", "name.go"} {
		if strings.Contains(string(b), leak) || strings.Contains(text, leak) {
			t.Fatalf("a path that could hold a secret or break a line is never printed whole (%q):\n%s", leak, text)
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "Security:") || strings.ContainsAny(line, "\x1b\t") {
			t.Fatalf("a path must not forge a line or carry a control character: %q", line)
		}
	}
	if !strings.Contains(text, "not read (too-deep): nimbalyst evil?Security: all clear/") {
		t.Fatalf("a control character is shown as ?:\n%s", text)
	}
	if strings.Contains(text, strings.Repeat("x", 300)) {
		t.Fatalf("a long name is cut, so one hostile path cannot flood the report")
	}
	if cov.Sources.Skipped["skipped:bad-name"] != 1 || !strings.Contains(text, "skipped:bad-name") {
		t.Fatalf("a bad name is counted, never named: %v", cov.Sources.Skipped)
	}
	if !strings.Contains(text, "withheld") {
		t.Fatalf("a withheld name says so:\n%s", text)
	}
	if cov.Sources.Skipped["skipped:secret"] != 2 {
		t.Fatalf("the rig's own secret skip and the shaped name are both still counted: %v", cov.Sources.Skipped)
	}
}

func TestCoverageLabelsProposalsAsExtractAdmissionsOnly(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	cov, err := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cov.Proposals.Basis, "extract") || !strings.Contains(cov.Proposals.Basis, "not verify") {
		t.Fatalf("the JSON says what the counts cover: %q", cov.Proposals.Basis)
	}
	if !strings.Contains(cov.Text(), "Extract admissions:") {
		t.Fatalf("the text report says what the counts cover:\n%s", cov.Text())
	}
}

func TestCoverageListsPagesAnEarlierBootstrapWroteThatWereNotRenderedThisTime(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	if err := r.RecordPublished(ctx, run.ID, map[string]string{"p/a.md": "h1", "p/gone-2.md": "h2", "p/evil\nforged.md": "h3"}); err != nil {
		t.Fatal(err)
	}
	cov, err := BuildCoverage(ctx, r, run.ID, []PageOutcome{{Path: "p/a.md", Outcome: "unchanged"}}, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(cov.StalePages) != 2 || cov.StalePages[0] != "p/evil?forged.md" || cov.StalePages[1] != "p/gone-2.md" {
		t.Fatalf("a recorded page that was not rendered is listed, sorted, with control characters shown as ?: %v", cov.StalePages)
	}
	text := cov.Text()
	if !strings.Contains(text, "p/gone-2.md") || !strings.Contains(text, "not deleted") {
		t.Fatalf("the text names the stale page and says it was left alone:\n%s", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "forged") {
			t.Fatalf("forged line %q", line)
		}
	}
}

func TestCoverageNamesClaimsThePageHoldsButTheIndexCannotRead(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	cov, err := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cov.Text(), "cannot read") {
		t.Fatalf("nothing to say when every claim is indexable:\n%s", cov.Text())
	}
	cov.Unindexed = []string{"clm_one", "clm_two"}
	if !strings.Contains(cov.Text(), "cannot read") || !strings.Contains(cov.Text(), "clm_two") {
		t.Fatalf("claim ids the index cannot read are named:\n%s", cov.Text())
	}
}

func TestPageLabelNamesAFixedPageAndOnlyTheHashOfAComponentPage(t *testing.T) {
	fixed := WikiDir + "/constraints-2.md"
	if got := PageLabel(fixed); got != fixed {
		t.Fatalf("a fixed page is named by its path: %q", got)
	}
	for _, prefix := range []string{"internal/queue", "secret-service/a.b"} {
		base := componentBase(prefix)
		for _, name := range []string{base + ".md", base + "-2.md", base + "-12.md"} {
			got := PageLabel(WikiDir + "/" + name)
			hash := shortSlugHash(prefix)
			if !strings.Contains(got, "component page") || !strings.Contains(got, hash) {
				t.Fatalf("%s: a component page is named by the hash suffix: %q", name, got)
			}
			for _, leak := range []string{"queue", "secret-service", "component-"} {
				if strings.Contains(got, leak) {
					t.Fatalf("%s: the component's name must not appear: %q", name, got)
				}
			}
		}
	}
	if got := PageLabel(WikiDir + "/something-unexpected\n.md"); strings.Contains(got, "\n") || strings.Contains(got, "something") {
		t.Fatalf("a page name that is neither fixed nor a component page is not printed: %q", got)
	}
}

// openItem leases one item of the run and returns it.
func openItem(t *testing.T, r *Runs, runID string) Item {
	t.Helper()
	b, err := r.Next(context.Background(), runID, 1)
	if err != nil || len(b.Items) != 1 {
		t.Fatalf("lease: %v %+v", err, b)
	}
	return b.Items[0]
}

func TestAnItemLeasedBeforeTheRunLeftRunningCannotCommitLater(t *testing.T) {
	ctx := context.Background()
	for _, to := range []string{StateFinishing, StateDone, StateAbandoned} {
		t.Run(to, func(t *testing.T) {
			r, run := coverageRig(t)
			it := openItem(t, r, run.ID)
			if _, err := r.DB.Exec(`UPDATE bs_runs SET state=? WHERE run_id=?`, to, run.ID); err != nil {
				t.Fatal(err)
			}
			plant := func(ctx context.Context, tx *sql.Tx, it Item) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier) VALUES(?,?,?,?,?,?,?,?,?,?)`,
					run.ID, r.CheckoutID, it.ID, 0, "clm_late", "Late.", "intent", "unverified", "", TierIntent)
				return err
			}
			if err := r.Complete(ctx, it.ID, it.Epoch, Usage{InputTokens: 5}, plant); !errors.Is(err, ErrState) {
				t.Fatalf("a late commit into a %s run must be refused as a state error, got %v", to, err)
			}
			if err := r.FailCounted(ctx, it.ID, it.Epoch, 1, 1, Usage{InputTokens: 5}); !errors.Is(err, ErrState) {
				t.Fatalf("a late failure into a %s run must be refused as a state error, got %v", to, err)
			}
			claims, _ := r.Claims(ctx, run.ID)
			got, _ := r.Get(ctx, run.ID)
			if len(claims) != 0 || got.TokensIn != 0 || got.Accepted != 0 || got.Rejected != 0 {
				t.Fatalf("nothing may be stored by a refused late commit: %d claims, %+v", len(claims), got)
			}
			after, _ := r.Item(ctx, it.ID)
			if after.State != "leased" {
				t.Fatalf("the item is left as it was: %+v", after)
			}
		})
	}
}

// Every page RenderPages can emit must get a label: a page that fell through to "unexpected"
// would hide which page held a secret. Split parts are included.
func TestEveryRenderedPageGetsALabelThatNeverRepeatsAComponentName(t *testing.T) {
	m := renderModel(t)
	for i := 0; i < 150; i++ {
		m.Claims = append(m.Claims,
			stored(t, "src:repo:docs/decisions/0001.md", "intent", "unverified", fmt.Sprintf("Statement %d %s", i, strings.Repeat("padding ", 60)), 100+i),
			stored(t, "comp:internal/queue", "current-impl", "unverified", fmt.Sprintf("Queue statement %d %s", i, strings.Repeat("padding ", 60)), 300+i))
	}
	pages := RenderPages(m)
	parts, comps := 0, 0
	for _, pg := range pages {
		got := PageLabel(pg.Path)
		switch {
		case strings.Contains(pg.Path, "/component-"):
			comps++
			if !strings.Contains(got, "a component page") || strings.Contains(got, "queue") || strings.Contains(got, "component-") {
				t.Errorf("%s: %q", pg.Path, got)
			}
		case got != pg.Path:
			t.Errorf("a fixed page is named by its path: %s -> %q", pg.Path, got)
		}
		if strings.Contains(pg.Path, "constraints-2") {
			parts++
		}
	}
	if parts == 0 || comps < 2 {
		t.Fatalf("the model must exercise split fixed pages and split component pages: %d %d", parts, comps)
	}
}

func TestCoverageSaysHowMuchWorkWasGivenUpOn(t *testing.T) {
	ctx := context.Background()
	r, run := coverageRig(t)
	cov, _ := BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if len(cov.GivenUp) != 0 || strings.Contains(cov.Text(), "given up") {
		t.Fatalf("nothing was given up yet: %v", cov.GivenUp)
	}
	if _, err := r.DB.Exec(`UPDATE bs_items SET state='rejected' WHERE run_id=? AND item_id IN (SELECT item_id FROM bs_items WHERE run_id=? LIMIT 1)`, run.ID, run.ID); err != nil {
		t.Fatal(err)
	}
	cov, _ = BuildCoverage(ctx, r, run.ID, nil, r.Now())
	if cov.GivenUp["extract"] != 1 || !strings.Contains(cov.Text(), "given up after 3 attempts: 1 extract") {
		t.Fatalf("an item that was dropped is a gap the report must show: %v\n%s", cov.GivenUp, cov.Text())
	}
}
