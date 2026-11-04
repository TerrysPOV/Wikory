package bootstrap

import (
	"fmt"
	"path"
	"regexp"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/redact"
)

var workName = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// WorkFiles stores the redacted text a subagent reads. Files live in the private store under
// bootstrap/<checkout id>/work/, so a clone rebind purges them, and are named by the hash of
// their input so an unchanged source reuses its file.
type WorkFiles struct {
	Root       *confine.Root
	CheckoutID string
}

// Rel is the store-relative path of a work file.
func (w WorkFiles) Rel(name string) string {
	return "bootstrap/" + w.CheckoutID + "/work/" + name + ".txt"
}

// Write redacts text and stores it with mode 0600. It returns how many redactions it made.
// Secret-bearing sources are skipped before this point, so a count above zero usually means
// a secret got past the scan; it can also come from a source that already contains the literal
// text "[REDACTED:", which redact.String rewrites, so treat the count as a prompt to look, not
// as proof of a leak. The file is named by the caller (an item's InputHash, which is derived
// from the original files' hashes, never from this file's bytes), so redaction does not
// change a work file's name.
func (w WorkFiles) Write(name, text string) (int, error) {
	if !workName.MatchString(name) {
		return 0, fmt.Errorf("bootstrap: %q is not a work file name", name)
	}
	clean, n := redactText(text)
	return n, w.put(name, clean)
}

// WriteDigest builds a component digest from files and stores it with mode 0600. Each file's
// text goes through redact.String BEFORE the digest is framed, and the assembled digest is not
// redacted again: redaction rules can match across a newline, so a file ending on an unset
// assignment would otherwise swallow the header line of the file after it and misattribute
// its evidence. Wikory's own header and omitted lines therefore sit outside any redaction pass,
// and every byte of file text has been through one. It returns the redactions made and the
// paths left out of the digest (see BuildDigest). The name is the item's InputHash, as for Write.
func (w WorkFiles) WriteDigest(name string, files []DigestFile, max int) (int, []string, error) {
	if !workName.MatchString(name) {
		return 0, nil, fmt.Errorf("bootstrap: %q is not a work file name", name)
	}
	n := 0
	clean := make([]DigestFile, len(files))
	for i, f := range files {
		text, c := redactText(f.Text)
		clean[i] = DigestFile{Path: f.Path, Hash: f.Hash, Text: text}
		n += c
	}
	text, omitted := BuildDigest(clean, max)
	return n, omitted, w.put(name, text)
}

func redactText(text string) (string, int) {
	clean, counts := redact.String(text)
	n := 0
	for _, c := range counts {
		n += c
	}
	return clean, n
}

// put stores already-redacted text.
func (w WorkFiles) put(name, clean string) error {
	rel := w.Rel(name)
	if err := w.Root.MkdirAll(path.Dir(rel), 0o700); err != nil {
		return err
	}
	return w.Root.WriteFileAtomic(rel, []byte(clean), 0o600)
}

// Read returns a work file's text, at most max bytes.
func (w WorkFiles) Read(name string, max int64) ([]byte, error) {
	if !workName.MatchString(name) {
		return nil, fmt.Errorf("bootstrap: %q is not a work file name", name)
	}
	return w.Root.ReadFile(w.Rel(name), max)
}
