package store

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// slack scales time bounds on a slow machine (WIKORY_LATENCY_SLACK, default 1).
func slack() time.Duration {
	if n, err := strconv.Atoi(os.Getenv("WIKORY_LATENCY_SLACK")); err == nil && n > 0 {
		return time.Duration(n)
	}
	return 1
}

func newWriteStore(t *testing.T) *Store {
	t.Helper()
	return open(t, t.TempDir(), ident("wkp_W", "github.com/o/w", "/w/.git", "/w"), true)
}

// holdWriteLockInAnotherProcess starts a helper process that holds the store's db-write lock
// until the returned release function is called.
func holdWriteLockInAnotherProcess(t *testing.T, path string) (release func()) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(exe, "-test.run=^$")
	c.Env = append(os.Environ(), "WIKORY_TEST_ROLE=write-lock-holder", "WIKORY_TEST_LOCK="+path)
	in, err := c.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(out).ReadString('\n')
	if line != "locked\n" {
		in.Close()
		_ = c.Wait()
		t.Fatalf("helper did not take the lock: %q", line)
	}
	var once sync.Once
	release = func() { once.Do(func() { in.Close(); _ = c.Wait() }) }
	t.Cleanup(release)
	return release
}

// TestWithWriteLockReturnsTimeoutWhileAnotherProcessHoldsTheLock is the explicit failure mode:
// a write that cannot get the lock fails with ErrWriteLockTimeout inside the caller's budget,
// and the write did not happen.
func TestWithWriteLockReturnsTimeoutWhileAnotherProcessHoldsTheLock(t *testing.T) {
	s := newWriteStore(t)
	release := holdWriteLockInAnotherProcess(t, s.WriteLockPath())
	budget := 600 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	start := time.Now()
	err := s.Bump(ctx, "blocked")
	el := time.Since(start)
	if !errors.Is(err, ErrWriteLockTimeout) {
		t.Fatalf("a write while another process held db-write.lock: want ErrWriteLockTimeout, got %v", err)
	}
	if el >= budget {
		t.Fatalf("the write waited %v, not inside its %v budget", el, budget)
	}
	release()
	if n, err := s.Counter(context.Background(), "blocked"); err != nil || n != 0 {
		t.Fatalf("the refused write happened anyway: n=%d err=%v", n, err)
	}
	if err := s.Bump(context.Background(), "blocked"); err != nil {
		t.Fatalf("after the holder released: %v", err)
	}
}

func TestWithWriteLockRefusesAReentrantCall(t *testing.T) {
	s := newWriteStore(t)
	var inner error
	err := s.WithWriteLock(context.Background(), func(ctx context.Context) error {
		inner = s.WithWriteLock(ctx, func(context.Context) error { return nil })
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(inner, ErrWriteLockReentered) {
		t.Fatalf("nested WithWriteLock: want ErrWriteLockReentered, got %v", inner)
	}
	// A nested store write goes through the same guard, so it fails instead of deadlocking.
	err = s.WithWriteLock(context.Background(), func(ctx context.Context) error { return s.Bump(ctx, "x") })
	if !errors.Is(err, ErrWriteLockReentered) {
		t.Fatalf("Bump inside WithWriteLock: want ErrWriteLockReentered, got %v", err)
	}
}

// The guard is per lock file, so the lock of one store is not "held" while another is taken.
func TestWithWriteLockGuardIsPerLockFile(t *testing.T) {
	a, b := newWriteStore(t), open(t, t.TempDir(), ident("wkp_X", "github.com/o/x", "/x/.git", "/x"), true)
	err := a.WithWriteLock(context.Background(), func(ctx context.Context) error {
		return b.WithWriteLock(ctx, func(context.Context) error { return nil })
	})
	if err != nil {
		t.Fatalf("a different store's lock is a different lock: %v", err)
	}
}

func TestWithWriteLockReleasesAfterAFailingCallback(t *testing.T) {
	s := newWriteStore(t)
	boom := errors.New("boom")
	if err := s.WithWriteLock(context.Background(), func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("the callback's error must come back unchanged, got %v", err)
	}
	u, err := Lock(s.WriteLockPath(), 0)
	if err != nil {
		t.Fatalf("the lock was still held after the callback failed: %v", err)
	}
	u()
}

// A callback that panics must not leave the lock held for the life of the process.
func TestWithWriteLockReleasesAfterAPanickingCallback(t *testing.T) {
	s := newWriteStore(t)
	func() {
		defer func() { _ = recover() }()
		_ = s.WithWriteLock(context.Background(), func(context.Context) error { panic("boom") })
	}()
	u, err := Lock(s.WriteLockPath(), 0)
	if err != nil {
		t.Fatalf("the lock was still held after the callback panicked: %v", err)
	}
	u()
}

// With the lock held, a call waits for it and then proceeds when it is released.
func TestWithWriteLockWaitsForARelease(t *testing.T) {
	s := newWriteStore(t)
	u, err := Lock(s.WriteLockPath(), 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { time.Sleep(150 * time.Millisecond); u() }()
	if err := s.Bump(context.Background(), "waited"); err != nil {
		t.Fatalf("a write that could wait for the holder failed: %v", err)
	}
}

// Every Store write method takes the lock: with it held elsewhere, each fails with
// ErrWriteLockTimeout and none of them touches the database.
func TestEveryStoreWriteTakesTheWriteLock(t *testing.T) {
	s := newWriteStore(t)
	id := ident("wkp_W", "github.com/o/w", "/w/.git", "/w")
	id.CloneID = "wkc_1"
	writes := map[string]func(ctx context.Context) error{
		"Bump":        func(ctx context.Context) error { return s.Bump(ctx, "n") },
		"SetCloneID":  func(ctx context.Context) error { return s.SetCloneID(ctx, "/w/.git", "wkc_2") },
		"RecordTrust": func(ctx context.Context) error { return s.RecordTrust(ctx, "/w/.git", TrustTrusted, "r") },
		"RebindClone": func(ctx context.Context) error { return s.RebindClone(ctx, "/w/.git", "wkc_3") },
		"Register":    func(ctx context.Context) error { _, err := s.Register(ctx, id); return err },
	}
	u, err := Lock(s.WriteLockPath(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer u()
	for name, w := range writes {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		err := w(ctx)
		cancel()
		if !errors.Is(err, ErrWriteLockTimeout) {
			t.Errorf("%s did not take db-write.lock: want ErrWriteLockTimeout, got %v", name, err)
		}
	}
}

// The wait is bounded by the caller's context, and finishes before the deadline so the caller
// can still report the failure.
func TestWriteLockWaitLeavesTimeBeforeTheDeadline(t *testing.T) {
	s := newWriteStore(t)
	u, err := Lock(s.WriteLockPath(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer u()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	err = s.Bump(ctx, "x")
	if !errors.Is(err, ErrWriteLockTimeout) {
		t.Fatalf("want ErrWriteLockTimeout, got %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("the wait ran to the context deadline (%v) instead of leaving time to report", time.Since(start))
	}
}

// Uncontended, taking and releasing the lock is cheap: it is held only for a write
// transaction, so it must not become the cost of a hook.
func TestUncontendedWriteLockIsCheap(t *testing.T) {
	s := newWriteStore(t)
	const n = 200
	start := time.Now()
	for i := 0; i < n; i++ {
		if err := s.WithWriteLock(context.Background(), func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	per := time.Since(start) / n
	if bound := 20 * time.Millisecond * slack(); per > bound {
		t.Fatalf("uncontended acquire+release took %v on average, want under %v", per, bound)
	}
}

// Several processes, each with its own Store handle, bump one counter concurrently. Every
// increment must land. This is the shape that lost writes on Windows without the lock (CI run
// 41370903722); on unix it passes with or without the lock, so the unix-reachable proof that
// each site takes the lock is TestEveryStoreWriteTakesTheWriteLock.
func TestParallelProcessesLoseNoCounterWrite(t *testing.T) {
	data := t.TempDir()
	s := open(t, data, ident("wkp_W", "github.com/o/w", "/w/.git", "/w"), true)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const procs, each = 6, 60
	cmds := make([]*exec.Cmd, procs)
	outs := make([]*strings.Builder, procs)
	for i := range cmds {
		c := exec.Command(exe, "-test.run=^$")
		c.Env = append(os.Environ(), "WIKORY_TEST_ROLE=bump-child", "WIKORY_TEST_DATA="+data, "WIKORY_TEST_N="+strconv.Itoa(each))
		outs[i] = &strings.Builder{}
		c.Stdout, c.Stderr = outs[i], outs[i]
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds[i] = c
	}
	for i, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Errorf("child %d failed: %v %s", i, err, outs[i].String())
		}
	}
	n, err := s.Counter(context.Background(), "parallel")
	if err != nil {
		t.Fatal(err)
	}
	if n != procs*each {
		t.Fatalf("counter = %d, want %d: writes were lost", n, procs*each)
	}
}

// Opening a store whose schema is current writes nothing, so it must not queue behind another
// process's write: a hook that only reads still opens the store.
func TestOpeningAnUpToDateStoreDoesNotWaitForTheWriteLock(t *testing.T) {
	data := t.TempDir()
	id := ident("wkp_W", "github.com/o/w", "/w/.git", "/w")
	s := open(t, data, id, true)
	u, err := Lock(s.WriteLockPath(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer u()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	s2, err := Resolve(ctx, data, id, false)
	if err != nil {
		t.Fatalf("opening an up-to-date store while db-write.lock is held: %v", err)
	}
	s2.Close()
}

// Upgrading an out-of-date schema is a write, so it takes the write lock.
func TestSchemaUpgradeTakesTheWriteLock(t *testing.T) {
	data := t.TempDir()
	id := ident("wkp_W", "github.com/o/w", "/w/.git", "/w")
	s := open(t, data, id, true)
	if _, err := s.DB.Exec(`UPDATE meta SET v = '0' WHERE k = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	u, err := Lock(s.WriteLockPath(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer u()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if s2, err := Resolve(ctx, data, id, false); !errors.Is(err, ErrWriteLockTimeout) {
		if err == nil {
			s2.Close()
		}
		t.Fatalf("upgrading an old schema with db-write.lock held: want ErrWriteLockTimeout, got %v", err)
	}
}
