package bootstrap

import (
	"context"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/ids"
)

func TestPrepareWorkWritesOneFilePerExtractSpecAndNothingForSkippedSources(t *testing.T) {
	ctx := context.Background()
	w, _ := newWork(t)
	files := map[string]string{
		"internal/queue/queue.go": "package queue\n\nfunc Ack() {}\n",
		"docs/decisions/0001.md":  "# Use acks\nAcks follow the handler.\n",
		"notes.md":                "pass" + "word=hunter2hunter2\n",
	}
	repo := &memReader{name: "repo", files: files}
	res, err := Inventory(ctx, []Reader{repo}, Options{MaxFileBytes: MaxFileBytes, Budget: 5})
	if err != nil {
		t.Fatal(err)
	}
	st, err := PrepareWork(ctx, []Reader{repo}, res, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	specs := ExtractSpecs(res)
	if st.Files != len(specs) {
		t.Fatalf("one work file per extract spec: wrote %d for %d specs", st.Files, len(specs))
	}
	for _, sp := range specs {
		b, err := w.Read(sp.InputHash, 1<<20)
		if err != nil {
			t.Fatalf("%s: %v", sp.Ref, err)
		}
		if strings.HasPrefix(sp.Ref, "src:") && ids.SHA256Hex(b) != sp.InputHash {
			t.Fatalf("a document's work file is its own text, so it hashes to its input hash: %s", sp.Ref)
		}
		if strings.HasPrefix(sp.Ref, "comp:") && !strings.Contains(string(b), "=== wikory-file internal/queue/queue.go ") {
			t.Fatalf("a component digest frames each file: %s", b)
		}
	}
	if st.Redactions != 0 {
		t.Fatalf("nothing secret should reach a work file: %d", st.Redactions)
	}
}

func TestPrepareWorkRefusesAFileThatChangedSinceInventory(t *testing.T) {
	ctx := context.Background()
	w, _ := newWork(t)
	repo := &memReader{name: "repo", files: map[string]string{"docs/decisions/0001.md": "# one\n"}}
	res, _ := Inventory(ctx, []Reader{repo}, Options{MaxFileBytes: MaxFileBytes, Budget: 5})
	repo.files["docs/decisions/0001.md"] = "# changed after the scan\n"
	if _, err := PrepareWork(ctx, []Reader{repo}, res, w, nil); err == nil {
		t.Fatal("content that no longer matches the scanned hash must not become a work file")
	}
}

// A file that ends on an unset assignment is clean on its own. Redacting the joined digest
// instead of each file lets the assignment's separator reach across the newline and swallow the
// next file's header, so b.go would lose its section and a false redaction notice would be raised.
func TestPrepareWorkKeepsEverySectionWhenAFileEndsOnAnUnsetAssignment(t *testing.T) {
	ctx := context.Background()
	w, _ := newWork(t)
	repo := &memReader{name: "repo", files: map[string]string{
		"internal/queue/a.go": "package queue\n// see config: pass" + "word=\n",
		"internal/queue/b.go": "package queue\n\nfunc B() {}\n",
		"docs/design.md":      "The internal/queue package acks after the handler.\n",
	}}
	res, err := Inventory(ctx, []Reader{repo}, Options{MaxFileBytes: MaxFileBytes, Budget: 5})
	if err != nil {
		t.Fatal(err)
	}
	if a := srcBy(res, "internal/queue/a.go"); a.Disposition != DispQueued {
		t.Fatalf("precondition: a.go is clean on its own: %+v", a)
	}
	st, err := PrepareWork(ctx, []Reader{repo}, res, w, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.Redactions != 0 {
		t.Fatalf("nothing in these files is a secret, so no redaction notice: %d", st.Redactions)
	}
	valid := map[string]string{}
	for _, s := range res.Sources {
		valid[s.Path] = short(s.Hash)
	}
	checked := false
	for _, sp := range ExtractSpecs(res) {
		if sp.Ref != "comp:internal/queue" {
			continue
		}
		checked = true
		b, err := w.Read(sp.InputHash, 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		secs := SectionsOf(string(b), valid)
		if len(secs) != 2 || secs[0] != (Section{"internal/queue/a.go", 2, 3}) || secs[1] != (Section{"internal/queue/b.go", 5, 7}) {
			t.Fatalf("each file keeps its own section: %+v\n%s", secs, b)
		}
	}
	if !checked {
		t.Fatal("expected a digest item for internal/queue")
	}
}
