package store

import (
	"context"
	"errors"
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
