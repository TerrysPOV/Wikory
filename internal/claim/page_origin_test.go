package claim

import "testing"

func TestPageOriginIsParsedAndNoLongerFlagged(t *testing.T) {
	src := "---\nid: x\ntype: concept\ntitle: X\norigin: bootstrap\n---\n# X\n"
	p := ParsePage("project-memory/wiki/x.md", []byte(src))
	if p.Origin != "bootstrap" {
		t.Fatalf("origin = %q", p.Origin)
	}
	for _, f := range p.Findings {
		t.Errorf("unexpected finding: %+v", f)
	}
	q := ParsePage("project-memory/wiki/y.md", []byte("---\nid: y\ncolour: blue\n---\n# Y\n"))
	if len(q.Findings) != 1 || q.Origin != "" {
		t.Fatalf("an unknown key is still flagged and a page without origin has none: %+v", q)
	}
}
