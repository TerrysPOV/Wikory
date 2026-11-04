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
