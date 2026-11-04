package store

import (
	"context"
	"path"
	"slices"
	"testing"
)

// Every table that carries a checkout_id must be in perCheckoutTables. A table added later and
// forgotten would survive a clone rebind, and the rebind notice says the data was discarded.
func TestEveryCheckoutKeyedTableIsInThePurgeList(t *testing.T) {
	s, _ := trustedCheckout(t)
	rows, err := s.DB.Query(`SELECT name FROM sqlite_master WHERE type IN ('table') AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	rows.Close()
	for _, n := range names {
		cols, err := s.DB.Query(`SELECT name FROM pragma_table_info(?)`, n)
		if err != nil {
			t.Fatal(err)
		}
		has := false
		for cols.Next() {
			var c string
			if err := cols.Scan(&c); err != nil {
				t.Fatal(err)
			}
			has = has || c == "checkout_id"
		}
		cols.Close()
		if has && !slices.Contains(perCheckoutTables, n) {
			t.Errorf("table %s has a checkout_id column but is not in perCheckoutTables", n)
		}
	}
}

// Two callers racing to prepare the verify stage must not queue the same component twice: each
// extra item is a paid verification. The table, not the callers, enforces it.
func TestAnItemRefIsUniquePerRunAndStage(t *testing.T) {
	s, _ := trustedCheckout(t)
	ins := `INSERT INTO bs_items(item_id,run_id,checkout_id,stage,ref,input_hash,state) VALUES(?,'r1','c','verify','comp:a','h','pending')`
	if _, err := s.DB.Exec(ins, "i1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ins, "i2"); err == nil {
		t.Fatal("a second verify item for the same component of the same run must be refused: that is two paid verifications of the same component")
	}
	other := `INSERT INTO bs_items(item_id,run_id,checkout_id,stage,ref,input_hash,state) VALUES('i3','r1','c','extract','comp:a','h','pending')`
	if _, err := s.DB.Exec(other); err != nil {
		t.Fatalf("the same ref at another stage is a different item: %v", err)
	}
}

func TestRebindPurgesBootstrapRowsAndWorkFiles(t *testing.T) {
	ctx := context.Background()
	s, id := trustedCheckout(t)
	co, ok, err := s.LookupCheckout(ctx, id)
	if err != nil || !ok {
		t.Fatalf("lookup: %v %v", ok, err)
	}
	ins := []string{
		`INSERT INTO bs_runs(run_id,checkout_id,state,plan_hash,scope,head_commit,sample,created_at,updated_at) VALUES('r1',?,'planned','h','all','c',0,'t','t')`,
		`INSERT INTO bs_sources(run_id,checkout_id,source,path,oid,tier,hash,size,disposition,secret_spans) VALUES('r1',?,'repo','a.go','o','implementation','h',1,'queued',0)`,
		`INSERT INTO bs_components(run_id,checkout_id,name,prefix,score,rank,inspected) VALUES('r1',?,'a','a',1,1,1)`,
		`INSERT INTO bs_items(item_id,run_id,checkout_id,stage,ref,input_hash,state) VALUES('i1','r1',?,'extract','a.go','h','pending')`,
		`INSERT INTO bs_claims(run_id,checkout_id,item_id,ordinal,claim_id,text,kind,status,meta,tier) VALUES('r1',?,'i1',0,'c1','t','current-impl','unverified','','implementation')`,
		`INSERT INTO bs_pages(checkout_id,path,hash,run_id) VALUES(?,'project-memory/wiki/bootstrap/overview.md','h','r1')`,
	}
	for _, q := range ins {
		if _, err := s.DB.Exec(q, co.ID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	work := "bootstrap/" + co.ID + "/r1/items/i1.txt"
	other := "bootstrap/someone-elses-checkout/r9/items/i.txt"
	for _, rel := range []string{work, other} {
		if err := s.Root.MkdirAll(path.Dir(rel), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.Root.WriteFileAtomic(rel, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RebindClone(ctx, id.CommonDir, "wkc_NEW"); err != nil {
		t.Fatal(err)
	}
	for _, tbl := range []string{"bs_runs", "bs_sources", "bs_components", "bs_items", "bs_claims", "bs_pages"} {
		var n int
		if err := s.DB.QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d rows survived a rebind (%v)", tbl, n, err)
		}
	}
	if _, err := s.Root.ReadFile(work, 10); err == nil {
		t.Error("the old checkout's work file survived a rebind")
	}
	if _, err := s.Root.ReadFile(other, 10); err != nil {
		t.Errorf("another checkout's work file was removed: %v", err)
	}
}
