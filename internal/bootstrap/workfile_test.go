package bootstrap

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/confine"
)

func newWork(t *testing.T) (WorkFiles, string) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root, err := confine.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return WorkFiles{Root: root, CheckoutID: "co1"}, dir
}

func TestWorkFileIsRedactedPrivateAndReadable(t *testing.T) {
	w, dir := newWork(t)
	secret := "hunter2hunter2"
	n, err := w.Write("aabbccdd", "line one\npass"+"word="+secret+"\nline three\n")
	if err != nil || n < 1 {
		t.Fatalf("a work file must be redacted even though secret-bearing sources are skipped earlier (defence in depth): n=%d err=%v", n, err)
	}
	b, err := w.Read("aabbccdd", 1<<20)
	if err != nil || strings.Contains(string(b), secret) || !strings.Contains(string(b), "[REDACTED:") {
		t.Fatalf("the work file must be redacted: %q %v", b, err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(dir, "bootstrap", "co1", "work", "aabbccdd.txt"))
		if err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("work files are 0600: %v %v", fi, err)
		}
	}
}

func TestWorkFileNamesAreHexOnly(t *testing.T) {
	w, _ := newWork(t)
	for _, bad := range []string{"", "../escape", "abc", "AABBCCDD", "aabbccdd/../../x", "aabbccdd.txt", strings.Repeat("a", 65)} {
		if _, err := w.Write(bad, "x"); err == nil {
			t.Errorf("Write(%q) should be refused", bad)
		}
		if _, err := w.Read(bad, 10); err == nil {
			t.Errorf("Read(%q) should be refused", bad)
		}
	}
}

func TestWritingTheSameWorkFileTwiceIsFine(t *testing.T) {
	w, _ := newWork(t)
	for i := 0; i < 2; i++ {
		if _, err := w.Write("aabbccdd", "same\n"); err != nil {
			t.Fatal(err)
		}
	}
}
