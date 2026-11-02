package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
)

// maxManifestRead bounds the manifest read while looking for the checkout a job belonged to.
const maxManifestRead = 1 << 20

// removeTree deletes rel and everything under it through the store's confined root, never
// following a link. A name it cannot remove is returned in failed; the rest is still removed.
func (s *Store) removeTree(rel string, failed *[]string) {
	ents, err := s.Root.ReadDir(rel)
	if err != nil {
		// rel is gone, or is a file or a link: unlink it as one.
		if rmErr := s.Root.Remove(rel); rmErr != nil && !isGone(rmErr) {
			*failed = append(*failed, rel)
		}
		return
	}
	for _, e := range ents {
		child := rel + "/" + e.Name()
		if e.IsDir() {
			s.removeTree(child, failed)
		} else if err := s.Root.Remove(child); err != nil && !isGone(err) {
			*failed = append(*failed, child)
		}
	}
	if err := s.Root.Remove(rel); err != nil && !isGone(err) {
		*failed = append(*failed, rel)
	}
}

// purgeCheckoutFiles removes the captured turns and the job directories of the given
// checkout ids from the private store. It is best effort: what cannot be removed becomes a
// notice, because the revocation it follows has already committed and must not be undone.
func (s *Store) purgeCheckoutFiles(oldIDs []string) {
	if len(oldIDs) == 0 {
		return
	}
	var failed []string
	for _, id := range oldIDs {
		s.removeTree("turns/"+id, &failed)
	}
	jobs, err := s.Root.ReadDir("jobs")
	if err != nil {
		if !isGone(err) {
			failed = append(failed, "jobs")
		}
	}
	for _, e := range jobs {
		if !e.IsDir() {
			continue
		}
		data, err := s.Root.ReadFile("jobs/"+e.Name()+"/manifest.json", maxManifestRead)
		if err != nil {
			continue // no readable manifest: the job names no checkout, so it is not this clone's
		}
		var m struct {
			CheckoutID string `json:"checkout_id"`
		}
		if json.Unmarshal(data, &m) == nil && slices.Contains(oldIDs, m.CheckoutID) {
			s.removeTree("jobs/"+e.Name(), &failed)
		}
	}
	if len(failed) > 0 {
		s.Notices = append(s.Notices, fmt.Sprintf("%d captured file(s) of the discarded checkouts could not be removed from the private store (first: %s); delete them by hand", len(failed), failed[0]))
	}
}

func isGone(err error) bool { return errors.Is(err, fs.ErrNotExist) }
