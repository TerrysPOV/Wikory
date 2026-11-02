package capture

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/ids"
)

var errOutsideRoots = errors.New("capture: transcript is outside the approved transcript roots")

func rootDigest(dir string) string { return ids.Digest("transcript-root", dir) }

// locate maps a live transcript path to a pointer below an approved root: the root's
// digest, the slash path relative to it (redacted like every persisted string), and a
// content-independent id. No absolute path is ever persisted (spec 10.3).
func (r *Recorder) locate(path string) (Pointer, bool) {
	if path == "" || !filepath.IsAbs(path) {
		return Pointer{}, false
	}
	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return Pointer{}, false
	}
	full := filepath.Join(dir, filepath.Base(path))
	for _, approved := range r.TranscriptRoots {
		resolved, err := filepath.EvalSymlinks(approved)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(resolved, full)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue
		}
		cleanRel := clean(filepath.ToSlash(rel), maxPath, map[string]int{})
		return newPointer(rootDigest(resolved), cleanRel), true
	}
	return Pointer{}, false
}

func newPointer(root, rel string) Pointer {
	return Pointer{Root: root, Rel: rel, ID: ids.Digest("transcript", root, rel)}
}

// encodePointer is the form stored in turn_starts.transcript_path: "<root digest>:<rel>".
func encodePointer(p Pointer) string {
	if p.Root == "" {
		return ""
	}
	return p.Root + ":" + p.Rel
}

func decodePointer(s string) Pointer {
	root, rel, ok := strings.Cut(s, ":")
	if !ok {
		return Pointer{}
	}
	return newPointer(root, rel)
}

// openPointer opens a transcript through the approved root whose digest the pointer names,
// with no-follow semantics below that root (spec 5.5 rule 7). A relative path that the
// redactor altered no longer names the file, so such a turn seals incomplete. The caller
// closes both.
func (r *Recorder) openPointer(p Pointer) (*os.File, *confine.Root, error) {
	if p.Root == "" || p.Rel == "" {
		return nil, nil, errOutsideRoots
	}
	for _, approved := range r.TranscriptRoots {
		root, err := confine.Open(approved)
		if err != nil {
			continue
		}
		if rootDigest(root.Path()) != p.Root {
			root.Close()
			continue
		}
		f, err := root.OpenRead(p.Rel)
		if err != nil {
			root.Close()
			return nil, nil, err
		}
		return f, root, nil
	}
	return nil, nil, errOutsideRoots
}

// transcriptSize is the current length of the live transcript, or 0 when it cannot be read.
func (r *Recorder) transcriptSize(path string) int64 {
	p, ok := r.locate(path)
	if !ok {
		return 0
	}
	f, root, err := r.openPointer(p)
	if err != nil {
		return 0
	}
	defer root.Close()
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0
	}
	return fi.Size()
}
