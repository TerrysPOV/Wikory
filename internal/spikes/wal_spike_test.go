//go:build spike

package spikes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	prod    bool   // each child writes through a real store.Store and store.WithWriteLock (the production path)
	gate    bool   // a failure of this variant fails the test on every platform
}

var walVariants = []walVariant{
	{name: "wal", journal: "WAL"},                                             // the original measurement: reported, never fails the test
	{name: "wal+lock", journal: "WAL", lock: true, gate: true},                // the mitigation: the store lock serialises writers
	{name: "wal+store.WithWriteLock", journal: "WAL", prod: true, gate: true}, // the production helper itself, on a real store
	{name: "delete", journal: "DELETE"},                                       // the alternative: rollback journal, no WAL
}

// storeIdentity is the project the production-path variant opens in every process.
var storeIdentity = store.Identity{ProjectID: "wkp_SPIKE", Origin: "github.com/o/spike", CommonDir: "/spike/.git", Worktree: "/spike"}

// walWriteRetries is how many times the production-path child retries one write that timed
// out waiting for the database write lock before it gives the row up as exhausted.
const walWriteRetries = 20

// busyText reports an error that is SQLite's own busy/locked refusal, which the write lock
// must make impossible.
func busyText(err error) bool {
	m := err.Error()
	return strings.Contains(m, "database is locked") || strings.Contains(m, "SQLITE_BUSY")
}

// walChildStore is walChild for the production path: its own store.Store, and every write
// transaction inside store.WithWriteLock, exactly as capture, publish and the indexer do it.
// The only permitted failure is store.ErrWriteLockTimeout, which production callers handle
// (hooks log "dblock"); the child retries the same write up to walWriteRetries times and
// reports what happened rather than dying. It prints
//
//	acked=<commits acknowledged> timeouts=<lock timeouts> busy=<SQLite busy errors>
//	exhausted=<rows given up> unheld=<writes that ran without the lock held>
func walChildStore() int {
	ctx := context.Background()
	s, err := store.Resolve(ctx, os.Getenv("WIKORY_SPIKE_DATA"), storeIdentity, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve:", err)
		return 1
	}
	defer s.Close()
	id := os.Getenv("WIKORY_SPIKE_CHILD")
	var lat []time.Duration
	acked, timeouts, busy, exhausted, unheld := 0, 0, 0, 0, 0
	report := func() {
		sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
		p50, p99 := int64(0), int64(0)
		if len(lat) > 0 {
			p50, p99 = lat[len(lat)/2].Microseconds(), lat[len(lat)*99/100].Microseconds()
		}
		fmt.Printf("p50=%d p99=%d acked=%d timeouts=%d busy=%d exhausted=%d unheld=%d\n", p50, p99, acked, timeouts, busy, exhausted, unheld)
	}
	defer report()
	for i := 0; i < walRowsPerChild; i++ {
		start := time.Now()
		write := func(ctx context.Context) error {
			// Proxy for "the lock is held at the write" that unix can fail: a second
			// acquisition of the same lock file must time out while this process holds it.
			if u, err := store.Lock(s.WriteLockPath(), 0); err == nil {
				u()
				unheld++
			}
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, `INSERT INTO t(child, n) VALUES(?, ?)`, id, i); err != nil {
				return err
			}
			return tx.Commit()
		}
		done := false
		for attempt := 0; attempt <= walWriteRetries && !done; attempt++ {
			err := s.WithWriteLock(ctx, write)
			switch {
			case err == nil:
				acked++
				done = true
			case errors.Is(err, store.ErrWriteLockTimeout):
				timeouts++
				time.Sleep(time.Duration(10*(attempt+1)) * time.Millisecond)
			case busyText(err):
				busy++
				done = true // SQLite refused the write: the lock did not protect it
			default:
				fmt.Fprintln(os.Stderr, "write:", err)
				return 1
			}
		}
		if !done {
			exhausted++
		}
		lat = append(lat, time.Since(start))
	}
	return 0
}

func walChild() int {
	if os.Getenv("WIKORY_SPIKE_DATA") != "" {
		return walChildStore()
	}
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
// refuted in CI (3 children failed, rows=1000 want 1600), so it never fails the test. So is
// DELETE. The wal+lock variant (the raw store lock held for a minute) fails the test on every
// platform if it loses a write.
//
// The production variant, wal+store.WithWriteLock, is judged on the property production needs,
// not on surviving the stress loop without a timeout: its line is
//
//	VARIANT wal+store.WithWriteLock: acked=<a>/<want> timeouts=<t> busy=<b>
//
// and the test fails only if SQLite ever reported busy, a write ran without the lock held,
// the rows in the database differ from the commits the children were told succeeded, or a row
// was still unwritten after its retries. A store.ErrWriteLockTimeout is permitted (production
// callers log "dblock" and carry on); its count is logged so the default wait can be tuned.
func TestWALParallelProcesses(t *testing.T) {
	for _, v := range walVariants {
		t.Run(v.name, func(t *testing.T) {
			r := runWALVariant(t, v)
			want := walChildren * walRowsPerChild
			if v.prod {
				fmt.Printf("VARIANT %s: acked=%d/%d timeouts=%d busy=%d\n", v.name, r.acked, want, r.timeouts, r.busy)
				t.Logf("%s: rows=%d exhausted=%d unheld=%d children_failed=%d; %d lock timeouts across %d writes (retries absorb them; if this is frequent, tune store.DefaultWriteLockWait)",
					v.name, r.rows, r.exhausted, r.unheld, r.failed, r.timeouts, want)
				switch {
				case r.failed > 0:
					t.Fatalf("MITIGATION FAILED %s: %d children died with an error other than a lock timeout", v.name, r.failed)
				case r.busy > 0:
					t.Fatalf("MITIGATION FAILED %s: busy=%d, SQLite refused a write the lock should have protected", v.name, r.busy)
				case r.unheld > 0:
					t.Fatalf("MITIGATION FAILED %s: unheld=%d writes ran without the write lock held", v.name, r.unheld)
				case r.rows != r.acked:
					t.Fatalf("MITIGATION FAILED %s: rows=%d != acked=%d, an acknowledged write was lost or an unacknowledged one appeared", v.name, r.rows, r.acked)
				case r.acked < want:
					t.Fatalf("MITIGATION FAILED %s: acked=%d < %d after %d retries per row (exhausted=%d)", v.name, r.acked, want, walWriteRetries, r.exhausted)
				}
				return
			}
			fmt.Printf("VARIANT %s: failed=%d rows=%d/%d\n", v.name, r.failed, r.rows, want)
			ok := r.failed == 0 && r.rows == want
			switch {
			case ok:
				t.Logf("%s: %d processes x %d immediate transactions, no loss, no busy failure", v.name, walChildren, walRowsPerChild)
			case v.gate:
				t.Fatalf("MITIGATION FAILED %s: %d children failed, rows=%d want %d", v.name, r.failed, r.rows, want)
			default:
				t.Logf("REFUTED %s: %d children failed, rows=%d want %d", v.name, r.failed, r.rows, want)
			}
		})
	}
}

// walResult is what one variant run measured. The counters after rows are the production
// children's own reports, summed; they are zero for the other variants.
type walResult struct {
	failed, rows                             int
	acked, timeouts, busy, exhausted, unheld int
}

var statRe = map[string]*regexp.Regexp{}

// stat reads the integer after "<name>=" in a child's report, 0 when the child printed none.
func stat(out, name string) int {
	re, ok := statRe[name]
	if !ok {
		re = regexp.MustCompile(`\b` + name + `=(\d+)`)
		statRe[name] = re
	}
	m := re.FindStringSubmatch(out)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// runWALVariant starts the children against one fresh database and returns how many failed and
// how many rows the database holds afterwards. A harness fault (the pragma did not take, a
// child did not start, a lock-using child took no locks) is fatal, so a variant label is never
// attached to a run that did not do what the label says.
func runWALVariant(t *testing.T, v walVariant) walResult {
	var r walResult
	dir := t.TempDir()
	path := filepath.Join(dir, "w.db")
	lockPath, dataDir := "", ""
	if v.lock {
		lockPath = filepath.Join(dir, "w.lock")
	}
	var db *sql.DB
	if v.prod {
		dataDir = filepath.Join(dir, "data")
		s, err := store.Resolve(context.Background(), dataDir, storeIdentity, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		db = s.DB
	} else {
		var err error
		if db, err = sql.Open("sqlite", walDSN(path, v.journal)); err != nil {
			t.Fatal(err)
		}
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
			"WIKORY_SPIKE_JOURNAL="+v.journal, "WIKORY_SPIKE_LOCK="+lockPath, "WIKORY_SPIKE_DATA="+dataDir)
		outs[i] = &strings.Builder{}
		c.Stdout, c.Stderr = outs[i], outs[i]
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = c
	}
	for i, c := range cmds {
		err := c.Wait() // the output is complete only after Wait
		out := outs[i].String()
		if err != nil {
			r.failed++
			t.Logf("child %d failed: %v %s", i, err, out)
		} else {
			t.Logf("child %d %s", i, strings.TrimSpace(out))
		}
		if v.prod {
			r.acked += stat(out, "acked")
			r.timeouts += stat(out, "timeouts")
			r.busy += stat(out, "busy")
			r.exhausted += stat(out, "exhausted")
			r.unheld += stat(out, "unheld")
		} else if v.lock && err == nil && !strings.Contains(out, "locks="+strconv.Itoa(walRowsPerChild)) {
			t.Fatalf("variant %s: child %d did not take the lock around every transaction: %s", v.name, i, out)
		}
	}
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&r.rows); err != nil {
		t.Fatal(err)
	}
	if !v.prod { // the store closes its own handle at cleanup
		db.Close()
	}
	return r
}
