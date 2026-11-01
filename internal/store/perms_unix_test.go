//go:build unix

package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestEnsurePrivateDirExactModeUnderPermissiveUmask(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	d := filepath.Join(t.TempDir(), "data", "wikory")
	if err := EnsurePrivateDir(d); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(d)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode=%v", fi.Mode().Perm())
	}
}

func TestEnsurePrivateDirTightensWiderMode(t *testing.T) {
	d := filepath.Join(t.TempDir(), "wide")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(d); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(d)
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode=%v", fi.Mode().Perm())
	}
}

func TestEnsurePrivateDirRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(link); err == nil {
		t.Fatal("a symlinked store directory must be refused")
	}
}

func TestDataDirOverrideMustBeAbsolute(t *testing.T) {
	if _, err := DataDir(func(string) string { return "relative/dir" }); err == nil {
		t.Fatal("a relative WIKORY_DATA_DIR must be refused")
	}
	d, err := DataDir(func(k string) string {
		if k == "WIKORY_DATA_DIR" {
			return "/abs/dir"
		}
		return ""
	})
	if err != nil || d != "/abs/dir" {
		t.Fatalf("%q %v", d, err)
	}
}
