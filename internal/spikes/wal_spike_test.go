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

	"github.com/TerrysPOV/Wikory/internal/store"
	_ "modernc.org/sqlite"
)

const walChildren, walRowsPerChild = 8, 200

func walDSN(path, journal string) string {
	return "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(" + journal + ")&_pragma=synchronous(FULL)&_txlock=immediate"
}

// walVariant is one way of running the same parallel-process workload. The original V04/V05
// question is the first; the other two are the mitigations the Windows result may need.
type walVariant struct {
	name    string
	journal string // journal_mode pragma value
	lock    bool   // each child takes the store Lock around every write transaction
	gate    bool   // a failure of this variant fails the test on every platform
}

var walVariants = []walVariant{
	{name: "wal", journal: "WAL"},                              // the original measurement: reported, never fails the test
	{name: "wal+lock", journal: "WAL", lock: true, gate: true}, // the mitigation: the store lock serialises writers
	{name: "delete", journal: "DELETE"},                        // the alternative: rollback journal, no WAL
}

func walChild() int {
	db, err := sql.Open("sqlite", walDSN(os.Getenv("WIKORY_SPIKE_DB"), os.Getenv("WIKORY_SPIKE_JOURNAL")))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer db.Close()
	id := os.Getenv("WIKORY_SPIKE_CHILD")
	lockPath := os.Getenv("WIKORY_SPIKE_LOCK") // empty: no lock
	var lat []time.Duration
	locks := 0
	for i := 0; i < walRowsPerChild; i++ {
		start := time.Now()
		unlock := func() {}
		if lockPath != "" {
			u, err := store.Lock(lockPath, time.Minute)
			if err != nil {
				fmt.Fprintln(os.Stderr, "lock:", err)
				return 1
			}
			unlock = u
			locks++
		}
		tx, err := db.Begin()
		if err != nil {
			unlock()
			fmt.Fprintln(os.Stderr, "begin:", err)
			return 1
		}
		if _, err := tx.Exec(`INSERT INTO t(child, n) VALUES(?, ?)`, id, i); err != nil {
			fmt.Fprintln(os.Stderr, "insert:", err)
			tx.Rollback()
			unlock()
			return 1
		}
		if err := tx.Commit(); err != nil {
			unlock()
			fmt.Fprintln(os.Stderr, "commit:", err)
			return 1
		}
		unlock()
		lat = append(lat, time.Since(start))
	}
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	fmt.Printf("p50=%d p99=%d locks=%d\n", lat[len(lat)/2].Microseconds(), lat[len(lat)*99/100].Microseconds(), locks)
	return 0
}

// TestWALParallelProcesses runs the same workload (8 processes x 200 immediate transactions,
// busy_timeout 5000) under each variant and prints one line per variant:
//
//	VARIANT <name>: failed=<n> rows=<m>/<want>
//
// The plain WAL variant is the V04/V05 question: it is reported, and on Windows it was
// refuted in CI (3 children failed, rows=1000 want 1600), so it never fails the test. The
// lock variant is the mitigation and fails the test on every platform if it loses a write.
func TestWALParallelProcesses(t *testing.T) {
	for _, v := range walVariants {
		t.Run(v.name, func(t *testing.T) {
			failed, rows := runWALVariant(t, v)
			want := walChildren * walRowsPerChild
			fmt.Printf("VARIANT %s: failed=%d rows=%d/%d\n", v.name, failed, rows, want)
			ok := failed == 0 && rows == want
			switch {
			case ok:
				t.Logf("%s: %d processes x %d immediate transactions, no loss, no busy failure", v.name, walChildren, walRowsPerChild)
			case v.gate:
				t.Fatalf("MITIGATION FAILED %s: %d children failed, rows=%d want %d", v.name, failed, rows, want)
			default:
				t.Logf("REFUTED %s: %d children failed, rows=%d want %d", v.name, failed, rows, want)
			}
		})
	}
}

// runWALVariant starts the children against one fresh database and returns how many failed and
// how many rows the database holds afterwards. A harness fault (the pragma did not take, a
// child did not start, a lock-using child took no locks) is fatal, so a variant label is never
// attached to a run that did not do what the label says.
func runWALVariant(t *testing.T, v walVariant) (failed, rows int) {
	dir := t.TempDir()
	path := filepath.Join(dir, "w.db")
	lockPath := ""
	if v.lock {
		lockPath = filepath.Join(dir, "w.lock")
	}
	db, err := sql.Open("sqlite", walDSN(path, v.journal))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE t(child TEXT NOT NULL, n INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(mode, v.journal) {
		t.Fatalf("variant %s: journal_mode is %q, want %q", v.name, mode, v.journal)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmds := make([]*exec.Cmd, walChildren)
	outs := make([]*strings.Builder, walChildren)
	for i := range cmds {
		c := exec.Command(exe, "-test.run=^$")
		c.Env = append(os.Environ(), "WIKORY_SPIKE_ROLE=wal-child", "WIKORY_SPIKE_DB="+path, "WIKORY_SPIKE_CHILD="+strconv.Itoa(i),
			"WIKORY_SPIKE_JOURNAL="+v.journal, "WIKORY_SPIKE_LOCK="+lockPath)
		outs[i] = &strings.Builder{}
		c.Stdout, c.Stderr = outs[i], outs[i]
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = c
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			failed++
			t.Logf("child %d failed: %v %s", i, err, outs[i].String())
			continue
		}
		t.Logf("child %d %s", i, strings.TrimSpace(outs[i].String()))
		if v.lock && !strings.Contains(outs[i].String(), "locks="+strconv.Itoa(walRowsPerChild)) {
			t.Fatalf("variant %s: child %d did not take the lock around every transaction: %s", v.name, i, outs[i].String())
		}
	}
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	db.Close()
	return failed, rows
}
