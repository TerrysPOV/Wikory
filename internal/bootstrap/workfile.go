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

// Write redacts text and stores it with mode 0600. It returns how many redactions it made;
// secret-bearing sources are skipped before this point, so a non-zero count is worth a notice.
func (w WorkFiles) Write(name, text string) (int, error) {
	if !workName.MatchString(name) {
		return 0, fmt.Errorf("bootstrap: %q is not a work file name", name)
	}
	clean, counts := redact.String(text)
	n := 0
	for _, c := range counts {
		n += c
	}
	rel := w.Rel(name)
	if err := w.Root.MkdirAll(path.Dir(rel), 0o700); err != nil {
		return 0, err
	}
	return n, w.Root.WriteFileAtomic(rel, []byte(clean), 0o600)
}

// Read returns a work file's text, at most max bytes.
func (w WorkFiles) Read(name string, max int64) ([]byte, error) {
	if !workName.MatchString(name) {
		return nil, fmt.Errorf("bootstrap: %q is not a work file name", name)
	}
	return w.Root.ReadFile(w.Rel(name), max)
}
