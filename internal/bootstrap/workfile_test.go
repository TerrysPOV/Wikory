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
		if _, _, err := w.WriteDigest(bad, nil, 10); err == nil {
			t.Errorf("WriteDigest(%q) should be refused", bad)
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

func TestTheWorkDirectoryIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	w, dir := newWork(t)
	if _, err := w.Write("aabbccdd", "x\n"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "bootstrap", "co1", "work"))
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("the work directory is 0700 so other users cannot list it: %v %v", fi, err)
	}
}

// WriteDigest redacts each file's text before the digest is framed and does not redact the
// assembled digest, so the secret goes and every header still maps to its file.
func TestWriteDigestRedactsEachFilesTextAndKeepsEverySection(t *testing.T) {
	w, _ := newWork(t)
	secret := "hunter2hunter2"
	files := []DigestFile{
		{Path: "a.go", Hash: "1111111111112222", Text: "package a\nconst k = \"pass" + "word=" + secret + "\"\n"},
		{Path: "b.go", Hash: "3333333333334444", Text: "package b\n"},
	}
	n, omitted, err := w.WriteDigest("aabbccdd", files, 1<<10)
	if err != nil || len(omitted) != 0 || n < 1 {
		t.Fatalf("a secret in a file must be counted as redacted: n=%d omitted=%v err=%v", n, omitted, err)
	}
	b, err := w.Read("aabbccdd", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), secret) || !strings.Contains(string(b), "[REDACTED:") {
		t.Fatalf("the digest must carry no secret text: %q", b)
	}
	secs := SectionsOf(string(b), map[string]string{"a.go": "111111111111", "b.go": "333333333333"})
	if len(secs) != 2 || secs[0] != (Section{"a.go", 2, 3}) || secs[1] != (Section{"b.go", 5, 5}) {
		t.Fatalf("every file keeps its section after redaction: %+v\n%s", secs, b)
	}
}
