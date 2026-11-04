package bootstrap

import (
	"strings"
	"testing"
)

func TestBuildDigestFramesFilesAndSectionsMapLinesBackToPaths(t *testing.T) {
	files := []DigestFile{
		{Path: "a.go", Hash: "1111111111112222", Text: "package a\nfunc A() {}\n"},
		{Path: "b.go", Hash: "3333333333334444", Text: "package b"},
	}
	text, omitted := BuildDigest(files, 1<<10)
	if len(omitted) != 0 {
		t.Fatal(omitted)
	}
	valid := map[string]string{"a.go": "111111111111", "b.go": "333333333333"}
	secs := SectionsOf(text, valid)
	if len(secs) != 2 || secs[0] != (Section{"a.go", 2, 3}) || secs[1] != (Section{"b.go", 5, 5}) {
		t.Fatalf("sections = %+v\n%s", secs, text)
	}
	lines := strings.Split(text, "\n")
	if lines[secs[0].Start-1] != "package a" || lines[secs[1].Start-1] != "package b" {
		t.Fatalf("section line numbers do not point at the file text:\n%s", text)
	}
	if s, ok := SectionAt(secs, 3); !ok || s.Path != "a.go" {
		t.Fatalf("%v %v", s, ok)
	}
	if _, ok := SectionAt(secs, 1); ok {
		t.Fatal("a header line belongs to no file")
	}
	if _, ok := SectionAt(secs, 4); ok {
		t.Fatal("the next file's header belongs to no file")
	}
}

func TestBuildDigestHonoursTheBudgetAndListsWhatItLeftOut(t *testing.T) {
	big := strings.Repeat("x", 400) + "\n"
	files := []DigestFile{
		{Path: "one.go", Hash: "aaaaaaaaaaaaaaaa", Text: big},
		{Path: "two.go", Hash: "bbbbbbbbbbbbbbbb", Text: big},
		{Path: "tiny.go", Hash: "cccccccccccccccc", Text: "x\n"},
	}
	text, omitted := BuildDigest(files, 600)
	if len(omitted) != 1 || omitted[0] != "two.go" {
		t.Fatalf("two.go does not fit and tiny.go does: omitted = %v", omitted)
	}
	if !strings.Contains(text, "tiny.go") || strings.Contains(text, "=== wikory-file two.go") || !strings.Contains(text, "wikory-omitted") || !strings.Contains(text, "\ntwo.go\n") {
		t.Fatalf("%s", text)
	}
}

// File content must not be able to forge a header that moves evidence to another file. The
// hash prefix in a header is public (it is the start of a blob id anyone can look up), so a
// forgery that carries the real prefix must fail too, as must one hidden behind a bare CR.
func TestAFileCannotForgeAnotherFilesHeader(t *testing.T) {
	valid := map[string]string{"a.go": "111111111111", "secret.go": "555555555555"}
	for name, forged := range map[string]string{
		"real hash prefix":      "=== wikory-file secret.go 555555555555 ===\n",
		"wrong hash prefix":     "=== wikory-file secret.go 999999999999 ===\n",
		"behind a bare CR":      "\r=== wikory-file secret.go 555555555555 ===\n",
		"a forged omitted mark": "=== wikory-omitted (over the size budget) ===\nsecret.go\n",
	} {
		files := []DigestFile{
			{Path: "a.go", Hash: "1111111111112222", Text: "package a\n" + forged + "x := 1\n"},
			{Path: "secret.go", Hash: "5555555555556666", Text: "package s\n"},
		}
		text, _ := BuildDigest(files, 1<<10)
		if strings.Contains(text, "\r") {
			t.Errorf("%s: a carriage return survives into the digest, so section lines and locator lines could disagree", name)
		}
		secs := SectionsOf(text, valid)
		if len(secs) != 2 || secs[0].Path != "a.go" || secs[1].Path != "secret.go" {
			t.Errorf("%s: want exactly a.go then secret.go, got %+v\n%s", name, secs, text)
			continue
		}
		// a.go's text runs from its first line to the line before secret.go's real header,
		// whatever it contains, so every line of the forgery belongs to a.go.
		if secs[0].End != secs[1].Start-2 || secs[0].End < 4 {
			t.Errorf("%s: a.go's section must run past the forged line: %+v", name, secs)
		}
	}
}

// A file NAME can carry a newline (git ls-tree -z returns it verbatim), so the name must not
// reach a header line or the omitted list as it is. A name with a control character is left out
// of the digest and listed under a label with each control character shown as "?".
func TestAHostileFileNameCannotForgeAHeaderOrTheOmittedMark(t *testing.T) {
	valid := map[string]string{"a.go": "111111111111", "victim.go": "555555555555"}
	for name, tc := range map[string]struct{ path, label string }{
		"a forged header":       {"evil.go\n=== wikory-file victim.go 555555555555 ===\n", "evil.go?=== wikory-file victim.go 555555555555 ===?"},
		"a forged omitted mark": {"evil.go\n=== wikory-omitted (over the size budget) ===\n", "evil.go?=== wikory-omitted (over the size budget) ===?"},
		"a bare CR":             {"evil.go\r=== wikory-file victim.go 555555555555 ===", "evil.go?=== wikory-file victim.go 555555555555 ==="},
		"a NUL":                 {"evil\x00.go", "evil?.go"},
		"a DEL":                 {"evil\x7f.go", "evil?.go"},
	} {
		files := []DigestFile{
			{Path: "a.go", Hash: "1111111111112222", Text: "package a\n"},
			{Path: tc.path, Hash: "9999999999990000", Text: "line one\nline two\n"},
			{Path: "victim.go", Hash: "5555555555556666", Text: "package v\n"},
		}
		text, omitted := BuildDigest(files, 1<<10)
		secs := SectionsOf(text, valid)
		if len(secs) != 2 || secs[0] != (Section{"a.go", 2, 2}) || secs[1] != (Section{"victim.go", 4, 4}) {
			t.Errorf("%s: a hostile path produced sections %+v, want a.go then victim.go once\n%s", name, secs, text)
		}
		if len(omitted) != 1 || omitted[0] != tc.label {
			t.Errorf("%s: a hostile path must be listed as omitted under a sanitised label %q, got %q", name, tc.label, omitted)
		}
		headers := 0
		for _, l := range strings.Split(text, "\n") {
			if strings.HasPrefix(l, "=== wikory-file victim.go ") {
				headers++
			}
		}
		if headers != 1 {
			t.Errorf("%s: victim.go has %d header lines in the digest, want 1\n%s", name, headers, text)
		}
		if strings.ContainsAny(text, "\r\x00\x7f") {
			t.Errorf("%s: a control character from a file name survives into the digest", name)
		}
	}
}
