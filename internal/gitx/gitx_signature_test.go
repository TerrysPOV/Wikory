package gitx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// forgeSignedCommit writes a commit on top of HEAD whose object carries a gpgsig header and moves
// HEAD to it. The signature is garbage: what matters is that git has something to verify, so a
// log that shows signatures runs gpg.program on it.
func forgeSignedCommit(t *testing.T, dir string) {
	t.Helper()
	tree := gitT(t, dir, "rev-parse", "HEAD^{tree}")
	parent := gitT(t, dir, "rev-parse", "HEAD")
	body := "tree " + tree + "\nparent " + parent +
		"\nauthor t <t@invalid> 1700000000 +0000\ncommitter t <t@invalid> 1700000000 +0000\n" +
		"gpgsig -----BEGIN PGP " + "SIGNATURE-----\n \n Zm9yZ2Vk\n -----END PGP " + "SIGNATURE-----\n\nforged\n"
	file := filepath.Join(t.TempDir(), "commit")
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	oid := gitT(t, dir, "hash-object", "-t", "commit", "-w", file)
	gitT(t, dir, "update-ref", "HEAD", oid)
}

// A repository's config can turn on log.showSignature and name its own gpg.program. Any
// `git log` that verifies a signature then runs that program. Churn and LastChanged read the log,
// so neither may ever verify a signature.
func TestLogReadersNeverRunTheRepositorysGpgProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the gpg.program stand-in is a shell script")
	}
	ctx := context.Background()
	dir := newRepo(t)
	forgeSignedCommit(t, dir)
	marker := filepath.Join(t.TempDir(), "PWNED-GPG")
	script := filepath.Join(t.TempDir(), "gpg.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > '"+marker+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, dir, "config", "gpg.program", script)
	gitT(t, dir, "config", "log.showSignature", "true")
	// Prove the trap is armed: a plain git log in this repository runs the script.
	gitT(t, dir, "log", "-1", "--format=%H")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the stand-in gpg.program never ran under a plain git log, so the test proves nothing: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	r, err := Discover(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Churn(ctx, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Churn ran the repository's gpg.program")
	}
	if _, err := r.LastChanged(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("LastChanged ran the repository's gpg.program")
	}
	// The safety arguments cover every log, not only the two readers that also pass
	// --no-show-signature themselves.
	if _, err := r.git(ctx, nil, "log", "-1", "--format=%H"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a plain log through Repo.git ran the repository's gpg.program")
	}
}

// gitEnv must switch off lazy fetching (a partial clone would otherwise reach the network for a
// missing blob) and replace objects (refs/replace can make HEAD's tree read as another tree).
func TestGitEnvSwitchesOffLazyFetchAndReplaceObjects(t *testing.T) {
	env := strings.Join(gitEnv(nil), "\n")
	for _, want := range []string{"GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1"} {
		if !strings.Contains(env, "\n"+want) {
			t.Errorf("gitEnv lacks %s", want)
		}
	}
}
