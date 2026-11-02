//go:build spike

package spikes

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestFTS5Available(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version string
	if err := db.QueryRow(`SELECT sqlite_version()`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("sqlite_version=%s", version)
	rows, err := db.Query(`PRAGMA compile_options`)
	if err != nil {
		t.Fatal(err)
	}
	var opts []string
	for rows.Next() {
		var o string
		if err := rows.Scan(&o); err != nil {
			t.Fatal(err)
		}
		opts = append(opts, o)
	}
	rows.Close()
	t.Logf("compile_options include ENABLE_FTS5: %v", strings.Contains(strings.Join(opts, ","), "ENABLE_FTS5"))

	if _, err := db.Exec(`CREATE VIRTUAL TABLE f USING fts5(title, body, tokenize='unicode61')`); err != nil {
		t.Fatalf("REFUTED V01: fts5 unavailable: %v", err)
	}
	_, err = db.Exec(`INSERT INTO f(title, body) VALUES
		('queue retries', 'nothing relevant here'),
		('unrelated', 'the enqueue job path retries on crash'),
		('enqueue job', 'title match should rank first')`)
	if err != nil {
		t.Fatal(err)
	}
	var top string
	err = db.QueryRow(`SELECT title FROM f WHERE f MATCH '"enqueue"' ORDER BY bm25(f, 10.0, 1.0) LIMIT 1`).Scan(&top)
	if err != nil {
		t.Fatalf("REFUTED V01: bm25 query failed: %v", err)
	}
	if top != "enqueue job" {
		t.Fatalf("REFUTED V01: column weighting not honoured, top=%q", top)
	}
	t.Log("VERIFIED V01: fts5 with bm25 column weights and unicode61")
}
