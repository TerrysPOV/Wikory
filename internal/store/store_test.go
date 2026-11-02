package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ident(project, origin, common, wt string) Identity {
	return Identity{ProjectID: project, Origin: origin, CommonDir: common, Worktree: wt}
}

func open(t *testing.T, data string, id Identity, create bool) *Store {
	t.Helper()
	s, err := Resolve(context.Background(), data, id, create)
	if err != nil {
		t.Fatalf("Resolve(%+v, create=%v): %v", id, create, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSSHAndHTTPSShareOneStore(t *testing.T) {
	data := t.TempDir()
	https, _ := CanonicalOrigin("https://github.com/o/r.git")
	ssh, _ := CanonicalOrigin("git@github.com:o/r.git")
	a := open(t, data, ident("wkp_P", https, "/c1/.git", "/c1"), true)
	b := open(t, data, ident("wkp_P", ssh, "/c2/.git", "/c2"), false)
	if a.Dir != b.Dir {
		t.Fatalf("%s != %s", a.Dir, b.Dir)
	}
}

func TestForkGetsSeparateStore(t *testing.T) {
	data := t.TempDir()
	a := open(t, data, ident("wkp_P", "github.com/o/r", "/c1/.git", "/c1"), true)
	if _, err := Resolve(context.Background(), data, ident("wkp_P", "github.com/fork/r", "/c2/.git", "/c2"), false); !errors.Is(err, ErrNoStore) {
		t.Fatalf("a fork must not find the original's store, got %v", err)
	}
	b := open(t, data, ident("wkp_P", "github.com/fork/r", "/c2/.git", "/c2"), true)
	if a.Dir == b.Dir {
		t.Fatal("fork shares the original's store")
	}
}

func TestAddingARemoteDoesNotOrphanTheStore(t *testing.T) {
	data := t.TempDir()
	ctx := context.Background()
	a := open(t, data, ident("wkp_P", "", "/c1/.git", "/c1"), true)
	if _, err := a.Register(ctx, ident("wkp_P", "", "/c1/.git", "/c1")); err != nil {
		t.Fatal(err)
	}
	b := open(t, data, ident("wkp_P", "github.com/o/r", "/c1/.git", "/c1"), false)
	if a.Dir != b.Dir {
		t.Fatal("adding a remote orphaned the store")
	}
	if !strings.Contains(strings.Join(b.Origins(), ","), "github.com/o/r") {
		t.Fatalf("new origin not recorded: %v", b.Origins())
	}
}

func TestCopiedOriginResolvesButDoesNotRegister(t *testing.T) {
	data := t.TempDir()
	ctx := context.Background()
	a := open(t, data, ident("wkp_P", "github.com/o/r", "/mine/.git", "/mine"), true)
	if _, err := a.Register(ctx, ident("wkp_P", "github.com/o/r", "/mine/.git", "/mine")); err != nil {
		t.Fatal(err)
	}
	// An archive that ships its own .git with a copied origin and project id.
	b := open(t, data, ident("wkp_P", "github.com/o/r", "/archive/.git", "/archive"), false)
	if _, ok, err := b.LookupCheckout(ctx, ident("wkp_P", "github.com/o/r", "/archive/.git", "/archive")); err != nil || ok {
		t.Fatalf("an unregistered checkout must stay unregistered: ok=%v err=%v", ok, err)
	}
}

func TestWorktreesAreSeparateCheckouts(t *testing.T) {
	data := t.TempDir()
	ctx := context.Background()
	s := open(t, data, ident("wkp_P", "github.com/o/r", "/c/.git", "/c"), true)
	if _, err := s.Register(ctx, ident("wkp_P", "github.com/o/r", "/c/.git", "/c")); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.LookupCheckout(ctx, ident("wkp_P", "github.com/o/r", "/c/.git", "/c-wt2")); ok {
		t.Fatal("a second worktree must not inherit the first worktree's registration")
	}
	if ok, _ := s.CommonDirRegistered(ctx, "/c/.git"); !ok {
		t.Fatal("the common directory should be registered")
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	data := t.TempDir()
	id := ident("wkp_P", "github.com/o/r", "/c/.git", "/c")
	s := open(t, data, id, true)
	if _, err := s.DB.Exec(`UPDATE meta SET v = '99' WHERE k = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Resolve(context.Background(), data, id, false); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("want ErrNewerSchema, got %v", err)
	}
}

func TestTrustRecordAndCounters(t *testing.T) {
	ctx := context.Background()
	s := open(t, t.TempDir(), ident("wkp_P", "", "/c/.git", "/c"), true)
	if _, ok, _ := s.TrustDecision(ctx, "/c/.git"); ok {
		t.Fatal("no decision should exist yet")
	}
	if err := s.RecordTrust(ctx, "/c/.git", TrustTrusted, "created by wikory init"); err != nil {
		t.Fatal(err)
	}
	d, ok, err := s.TrustDecision(ctx, "/c/.git")
	if err != nil || !ok || d != TrustTrusted {
		t.Fatalf("%q %v %v", d, ok, err)
	}
	_ = s.Bump(ctx, "hook_errors")
	_ = s.Bump(ctx, "hook_errors")
	if n, _ := s.Counter(ctx, "hook_errors"); n != 2 {
		t.Fatalf("counter=%d", n)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "store.toml")); err != nil {
		t.Fatal(err)
	}
}

// Two clones of one project, then the second gains the first one's origin. The second clone
// is still found by its own common directory, not flipped onto the first clone's store, and
// the shared origin is not copied into its registry. The key order of the two stores decides
// which one a first-match search sees first, so each round uses fresh paths.
func TestLateAddedRemoteNeverFlipsACloneOntoAnotherStore(t *testing.T) {
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		data := t.TempDir()
		a := ident("wkp_P", "github.com/o/r", fmt.Sprintf("/a%d/.git", i), fmt.Sprintf("/a%d", i))
		b := ident("wkp_P", "", fmt.Sprintf("/b%d/.git", i), fmt.Sprintf("/b%d", i))
		sa := open(t, data, a, true)
		if _, err := sa.Register(ctx, a); err != nil {
			t.Fatal(err)
		}
		sb := open(t, data, b, true)
		if _, err := sb.Register(ctx, b); err != nil {
			t.Fatal(err)
		}
		if sa.Dir == sb.Dir {
			t.Fatalf("round %d: the clones share a store from the start", i)
		}
		b.Origin = "github.com/o/r" // the remote is added later
		again, err := Resolve(ctx, data, b, false)
		if err != nil {
			t.Fatal(err)
		}
		if again.Dir != sb.Dir {
			t.Fatalf("round %d: the clone was flipped onto another clone's store (%s, want %s)", i, again.Dir, sb.Dir)
		}
		if strings.Contains(strings.Join(again.Origins(), ","), "github.com/o/r") || len(again.Notices) == 0 {
			t.Fatalf("round %d: the origin another store owns was aliased, or not reported: origins=%v notices=%v", i, again.Origins(), again.Notices)
		}
		if _, err := again.Register(ctx, b); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(again.Origins(), ","), "github.com/o/r") {
			t.Fatalf("round %d: Register aliased an origin another store owns: %v", i, again.Origins())
		}
		if _, ok, _ := again.LookupCheckout(ctx, b); !ok {
			t.Fatalf("round %d: the clone lost its registration", i)
		}
		again.Close()
		if back, err := Resolve(ctx, data, a, false); err != nil || back.Dir != sa.Dir {
			t.Fatalf("round %d: the first clone moved: %v", i, err)
		} else {
			back.Close()
		}
	}
}

// A store written by a newer wikory is refused before this binary changes anything in it:
// not its registry, not its schema.
func TestNewerSchemaIsRefusedBeforeAnyWrite(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	id := ident("wkp_P", "", "/c/.git", "/c")
	s := open(t, data, id, true)
	if _, err := s.Register(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE meta SET v = '99' WHERE k = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	// Rename a table, so this binary's schema would create it afresh if it ran.
	if _, err := s.DB.Exec(`ALTER TABLE counters RENAME TO counters_from_the_future`); err != nil {
		t.Fatal(err)
	}
	registryPath := filepath.Join(s.Dir, "store.toml")
	before, err := os.ReadFile(registryPath)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	id.Origin = "github.com/o/r" // would be added to the registry of a store this binary may write
	if _, err := Resolve(ctx, data, id, false); !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("want ErrNewerSchema, got %v", err)
	}
	after, err := os.ReadFile(registryPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("a refused store's registry was rewritten: %v\nbefore: %s\nafter: %s", err, before, after)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(s.Dir, "wikory.db"))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'counters'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the schema ran against a refused store: counters table present=%d err=%v", n, err)
	}
}
