package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/claim"
)

func stampRepo(t *testing.T, page string) (string, *cli) {
	t.Helper()
	dir := newGitRepo(t)
	_ = os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "src", "q.go"), []byte("package src\n"), 0o644)
	gitIn(t, dir, "add", "src/q.go")
	gitIn(t, dir, "commit", "-qm", "code")
	c := newCLI(t)
	if _, e, code := c.run(dir, "init"); code != ExitOK {
		t.Fatal(e)
	}
	_ = os.WriteFile(filepath.Join(dir, "project-memory", "wiki", "q.md"), []byte(page), 0o644)
	return dir, c
}

const handPage = "# Queue\n\n<!-- claim kind=current-impl status=verified ev=src/q.go dep=src/q.go -->\nQueue workers drain FIFO.\n"

func TestStampResolvesEvidenceAndMintsMetadata(t *testing.T) {
	dir, c := stampRepo(t, handPage)
	if _, e, code := c.run(dir, "page", "stamp", "project-memory/wiki/q.md"); code != ExitOK {
		t.Fatal(e)
	}
	got := readFile(t, filepath.Join(dir, "project-memory", "wiki", "q.md"))
	oid := gitIn(t, dir, "rev-parse", "HEAD:src/q.go")
	for _, want := range []string{"ev=src/q.go@sha1:" + oid, "status=verified", "scope=repo", "author=human", "th="} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %s", want, got)
		}
	}
	if !regexp.MustCompile(`c=[0-9A-HJKMNP-TV-Z]{26} `).MatchString(got) || !regexp.MustCompile(`vrev=[0-9a-f]{12} `).MatchString(got) {
		t.Fatalf("no minted id or vrev: %s", got)
	}
	if strings.Count(readFile(t, filepath.Join(dir, "project-memory", "log.md")), "stamp project-memory/wiki/q.md") != 1 {
		t.Fatal("no log line")
	}
	out, _, _ := c.run(dir, "search", "fifo")
	if !strings.Contains(out, "[verified | verified @") || !strings.Contains(out, "sources unchanged") {
		t.Fatalf("search after stamp: %s", out)
	}
}

func TestStampDowngradesUncommittedEvidence(t *testing.T) {
	dir, c := stampRepo(t, "# Q\n\n<!-- claim kind=current-impl status=verified ev=src/new.go dep=src/new.go -->\nNew code path.\n")
	_ = os.WriteFile(filepath.Join(dir, "src", "new.go"), []byte("package src\n"), 0o644) // present but not committed
	_, errOut, code := c.run(dir, "page", "stamp", "project-memory/wiki/q.md")
	got := readFile(t, filepath.Join(dir, "project-memory", "wiki", "q.md"))
	if code != ExitOK || !strings.Contains(got, "status=unverified") || strings.Contains(got, "ev=src/new.go@") || !strings.Contains(errOut, "not committed") {
		t.Fatalf("code=%d page=%s stderr=%s", code, got, errOut)
	}
}

func TestStampIsIdempotent(t *testing.T) {
	dir, c := stampRepo(t, handPage)
	c.run(dir, "page", "stamp", "project-memory/wiki/q.md")
	first := readFile(t, filepath.Join(dir, "project-memory", "wiki", "q.md"))
	c.run(dir, "page", "stamp", "project-memory/wiki/q.md")
	if readFile(t, filepath.Join(dir, "project-memory", "wiki", "q.md")) != first ||
		strings.Count(readFile(t, filepath.Join(dir, "project-memory", "log.md")), "stamp ") != 1 {
		t.Fatal("a second stamp changed the page or logged again")
	}
}

func TestStampRefusesPathsOutsideTheWiki(t *testing.T) {
	dir, c := stampRepo(t, handPage)
	// README.md is a real markdown file outside the wiki: only the wiki-prefix check refuses it.
	for _, p := range []string{"../x.md", "project-memory/config.toml", "/etc/hosts", "src/q.go", "README.md"} {
		if _, _, code := c.run(dir, "page", "stamp", p); code == ExitOK {
			t.Errorf("%q accepted", p)
		}
	}
}

// Rule 2 for a tracked file: the evidence is committed but its working copy has changed, so
// stamping must not take the HEAD blob as evidence for what the author was looking at.
func TestStampKeepsTrackedButDirtyEvidenceUnverified(t *testing.T) {
	dir, c := stampRepo(t, handPage)
	_ = os.WriteFile(filepath.Join(dir, "src", "q.go"), []byte("package src\n// edited, not committed\n"), 0o644)
	_, errOut, code := c.run(dir, "page", "stamp", "project-memory/wiki/q.md")
	got := readFile(t, filepath.Join(dir, "project-memory", "wiki", "q.md"))
	if code != ExitOK || !strings.Contains(got, "status=unverified") || strings.Contains(got, "ev=src/q.go@") || !strings.Contains(errOut, "uncommitted changes") {
		t.Fatalf("code=%d page=%s stderr=%s", code, got, errOut)
	}
}

// A percent-encoded comma in a path used to be rendered back as a raw comma, which the
// parser rejects, so the stamped claim dropped out of the index.
func TestStampKeepsPercentEncodedPunctuationParseable(t *testing.T) {
	page := "# Q\n\n<!-- claim kind=intent status=unverified dep=src/a%2Cb.go#s%28x%29 sup=old%26new -->\nA claim about odd names.\n"
	dir, c := stampRepo(t, page)
	if _, e, code := c.run(dir, "page", "stamp", "project-memory/wiki/q.md"); code != ExitOK {
		t.Fatal(e)
	}
	got := readFile(t, filepath.Join(dir, "project-memory", "wiki", "q.md"))
	p := claim.ParsePage("project-memory/wiki/q.md", []byte(got))
	if len(p.Blocks) != 1 || p.Blocks[0].Err != nil {
		t.Fatalf("the stamped claim no longer parses: %+v\n%s", p.Blocks, got)
	}
	if want := []string{"src/a,b.go#s(x)"}; !reflect.DeepEqual(p.Blocks[0].Claim.Deps, want) {
		t.Fatalf("deps %v, want %v\n%s", p.Blocks[0].Claim.Deps, want, got)
	}
}

// renderChecked is the stamp command's last line of defence: a claim whose rendering the
// parser would reject is refused instead of being written into the page.
func TestRenderCheckedRefusesALineThatDoesNotReparse(t *testing.T) {
	bad := claim.Claim{Kind: "intent", Status: "unverified", AKA: []string{""}}
	if _, err := claim.ParseComment(bad.Render()); err == nil {
		t.Fatal("test premise broken: the bad claim renders to a parseable line")
	}
	if line, err := renderChecked(bad); err == nil || line != "" || !errors.Is(err, claim.ErrMalformed) {
		t.Fatalf("line=%q err=%v", line, err)
	}
	good := claim.Claim{Kind: "intent", Status: "unverified"}
	if line, err := renderChecked(good); err != nil || line != good.Render() {
		t.Fatalf("line=%q err=%v", line, err)
	}
}
