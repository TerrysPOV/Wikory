// Package deps is the dependency register. Every third-party module the binary may use
// is imported here once, with the reason it is allowed. A module that is not listed here
// must not be added without an entry and a recorded decision (spec 10.5: minimal
// dependencies, pinned go.sum).
package deps

import (
	// modernc.org/sqlite: pure-Go SQLite with FTS5, so the binary stays static with no cgo
	// on darwin, linux and windows (spec 3, 7.1). FTS5 availability is verified in V01.
	_ "modernc.org/sqlite"

	// github.com/pelletier/go-toml/v2: parses the committed config.toml and the store
	// registry. Chosen over BurntSushi/toml for DisallowUnknownFields, so a typo or an
	// unexpected capability request in a repository's config is an error, not silence.
	_ "github.com/pelletier/go-toml/v2"

	// golang.org/x/sys: openat with O_NOFOLLOW per path component, renameat, linkat, flock
	// on unix and LockFileEx on windows (spec 5.5 rule 7, 4.5 publish lock). Already a
	// transitive dependency of modernc.org/sqlite. The cpu package is imported here only
	// because it builds on every platform; the real imports are in confine and store.
	_ "golang.org/x/sys/cpu"

	// golang.org/x/text: Unicode NFC for the canonical claim text that th hashes (spec 5.3).
	// Not in the original allow-list; see plan adjudication Q4.
	_ "golang.org/x/text/unicode/norm"

	// github.com/yuin/goldmark v1.8.6 (MIT, pure Go, no further dependencies): a real
	// CommonMark parser, used by internal/claim to build the indexed page body from the text a
	// renderer shows. Added under spec 10.5 (minimal dependencies) because three rounds of
	// hand-written stripping of comments and link reference definitions were each bypassed in
	// review; the failure direction of any approximation of CommonMark is hidden text reaching
	// the index. Pinned to a release more than two weeks old when chosen.
	_ "github.com/yuin/goldmark"
)
