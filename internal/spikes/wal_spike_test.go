//go:build spike

package spikes

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const walChildren, walRowsPerChild = 8, 200

func walDSN(path string) string {
	return "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_txlock=immediate"
}

func walChild() int {
	db, err := sql.Open("sqlite", walDSN(os.Getenv("WIKORY_SPIKE_DB")))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	id := os.Getenv("WIKORY_SPIKE_CHILD")
	var lat []time.Duration
	for i := 0; i < walRowsPerChild; i++ {
		start := time.Now()
		tx, err := db.Begin()
		if err != nil {
			fmt.Fprintln(os.Stderr, "begin:", err)
			return 1
		}
		if _, err := tx.Exec(`INSERT INTO t(child, n) VALUES(?, ?)`, id, i); err != nil {
			fmt.Fprintln(os.Stderr, "insert:", err)
			tx.Rollback()
			return 1
		}
		if err := tx.Commit(); err != nil {
			fmt.Fprintln(os.Stderr, "commit:", err)
			return 1
		}
		lat = append(lat, time.Since(start))
	}
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	fmt.Printf("p50=%d p99=%d\n", lat[len(lat)/2].Microseconds(), lat[len(lat)*99/100].Microseconds())
	return 0
}

func TestWALParallelProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "w.db")
	db, err := sql.Open("sqlite", walDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t(child TEXT NOT NULL, n INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmds := make([]*exec.Cmd, walChildren)
	outs := make([]*strings.Builder, walChildren)
	for i := range cmds {
		c := exec.Command(exe, "-test.run=^$")
		c.Env = append(os.Environ(), "WIKORY_SPIKE_ROLE=wal-child", "WIKORY_SPIKE_DB="+path, "WIKORY_SPIKE_CHILD="+strconv.Itoa(i))
		outs[i] = &strings.Builder{}
		c.Stdout, c.Stderr = outs[i], outs[i]
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = c
	}
	failed := 0
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			failed++
			t.Logf("child %d failed: %v %s", i, err, outs[i].String())
		} else {
			t.Logf("child %d %s", i, strings.TrimSpace(outs[i].String()))
		}
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if failed > 0 || n != walChildren*walRowsPerChild {
		t.Fatalf("REFUTED V04: %d children failed, rows=%d want %d", failed, n, walChildren*walRowsPerChild)
	}
	t.Logf("VERIFIED V04: %d processes x %d immediate transactions, no loss, no busy failure", walChildren, walRowsPerChild)
}
