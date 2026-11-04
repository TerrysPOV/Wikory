package bootstrap

import (
	"bytes"
	"path"
	"strings"
)

// MaxFileBytes is the largest file bootstrap reads. A larger one is skipped, not truncated.
const MaxFileBytes = 256 << 10

const (
	binaryProbe = 8000
	headerProbe = 1024
)

var vendorDirs = map[string]bool{"vendor": true, "node_modules": true, "third_party": true,
	".git": true, ".hg": true, ".svn": true, "__pycache__": true}

var generatedBases = map[string]bool{"go.sum": true, "package-lock.json": true, "yarn.lock": true,
	"pnpm-lock.yaml": true, "cargo.lock": true, "poetry.lock": true, "uv.lock": true}

// Exclusion says why a file must not be read, or "" when it may be. head is the start of the
// file when it has been read and nil when only the name and size are known; the name and size
// checks run either way, so a caller can drop a file before reading it.
func Exclusion(p string, size int64, head []byte) string {
	if size > MaxFileBytes {
		return "skipped:oversize"
	}
	if strings.HasPrefix(p, "project-memory/") {
		return "skipped:wikory" // Wikory's own output is never its input
	}
	for _, seg := range strings.Split(p, "/") {
		if vendorDirs[strings.ToLower(seg)] {
			return "skipped:vendored"
		}
	}
	base := strings.ToLower(path.Base(p))
	if generatedBases[base] || strings.HasSuffix(base, ".min.js") || strings.HasSuffix(base, ".min.css") || strings.HasSuffix(base, ".map") {
		return "skipped:generated"
	}
	if len(head) > binaryProbe {
		if bytes.IndexByte(head[:binaryProbe], 0) >= 0 {
			return "skipped:binary"
		}
	} else if bytes.IndexByte(head, 0) >= 0 {
		return "skipped:binary"
	}
	h := head
	if len(h) > headerProbe {
		h = h[:headerProbe]
	}
	if bytes.Contains(h, []byte("Code generated")) && bytes.Contains(h, []byte("DO NOT EDIT")) || bytes.Contains(h, []byte("@generated")) {
		return "skipped:generated"
	}
	return ""
}
