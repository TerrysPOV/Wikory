package bootstrap

import (
	"strconv"
	"strings"
	"unicode"
)

const (
	headerPrefix = "=== wikory-file "
	headerSuffix = " ==="
	omittedMark  = "=== wikory-omitted (over the size budget) ==="
)

// DigestFile is one file going into a component digest. Hash is the file's content hash.
type DigestFile struct{ Path, Hash, Text string }

// Section is the line range of one file's text inside a digest.
type Section struct {
	Path       string
	Start, End int
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// unprintable is the one rule for a character that is never printed or written raw: a control
// character (newline, CR, NUL, DEL and the rest of unicode.IsControl), a format character such as
// U+202E (which reverses how the text after it is shown), or a line or paragraph separator. Every
// printer of text from the project or from a subagent (digest headers, claim text, rejection
// messages, report labels) uses it.
func unprintable(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}

// maxShownRunes bounds one printed name or message taken from the project or a subagent, so a
// hostile one cannot flood a report or a transcript.
const maxShownRunes = 200

// hasControl reports whether a path contains an unprintable character (see unprintable). Git
// returns file names verbatim, so a name can carry a newline or a U+2028; such a name must not be
// written into a header line or the omitted list.
func hasControl(p string) bool {
	return strings.IndexFunc(p, unprintable) >= 0
}

// unconfinable reports a path that confine.Split refuses on every platform: one holding a
// backslash, or with a colon as its second byte (it reads as a drive letter). Git allows both in
// a file name, but evidence for a claim is resolved through confine, so every claim from such a
// file would be refused after its extraction was paid for. The platform-specific reserved names
// confine also refuses are left out, so the inventory, and the plan hash, are the same on every
// platform.
func unconfinable(p string) bool {
	return strings.ContainsRune(p, '\\') || (len(p) >= 2 && p[1] == ':')
}

// visiblePath is a path with each unprintable character shown as "?", safe to list.
func visiblePath(p string) string {
	return strings.Map(func(r rune) rune {
		if unprintable(r) {
			return '?'
		}
		return r
	}, p)
}

func headerLine(f DigestFile) string {
	return headerPrefix + f.Path + " " + short(f.Hash) + headerSuffix
}

// headerBytes is what a file's header line costs in a digest, newline included, when its hash is
// a full content hash (12 characters are shown). The estimator uses it so the digest it sizes
// is the digest BuildDigest writes.
func headerBytes(path string) int {
	return len(headerPrefix) + len(path) + 1 + 12 + len(headerSuffix) + 1
}

// omittedReserve is the room kept for the omitted mark and the "and K more" line, so the list of
// left-out paths can never push a digest past its budget, however many files were left out.
const omittedReserve = len(omittedMark) + 1 + len("=== and 9223372036854775807 more ===\n")

func moreLine(k int) string { return "=== and " + strconv.Itoa(k) + " more ===\n" }

// frame keeps file text from posing as digest structure. Line endings become LF, so the digest's
// lines are the lines every other check counts, and a line that starts like a header or the
// omitted marker gets a leading space. After framing no line of any file begins where a header
// does, so SectionsOf can trust every header it finds. (The hash prefix in a header is public, so
// it could never be what stopped a forgery.)
func frame(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, headerPrefix) || strings.HasPrefix(l, omittedMark) {
			lines[i] = " " + l
		}
	}
	return strings.Join(lines, "\n")
}

// BuildDigest concatenates files under header lines, in the order given, until the budget is
// spent. A file that does not fit is left out whole and listed at the end; a smaller file that
// comes later may still fit. Each file's text is framed first (see frame). A file whose path
// holds a control character is never written: it is left out and listed by its visiblePath, so
// neither a header nor the omitted list can be forged from a file name. The caller redacts
// each file's text before calling (see WorkFiles.WriteDigest); BuildDigest redacts nothing.
//
// The whole digest, header lines and the omitted list included, is at most max bytes (for any
// max of at least omittedReserve). When anything is left out, omittedReserve bytes are kept for
// the omitted mark and an "and K more" line, and the list names as many left-out paths as the
// remaining room holds. The returned slice still lists every path left out.
func BuildDigest(files []DigestFile, max int) (string, []string) {
	type part struct{ head, body string }
	parts := make([]part, len(files))
	need, unwritable := 0, false
	for i, f := range files {
		if hasControl(f.Path) {
			unwritable = true
			continue
		}
		body := frame(f.Text)
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		parts[i] = part{headerLine(f) + "\n", body}
		need += len(parts[i].head) + len(body)
	}
	budget := max
	if unwritable || need > max {
		budget = max - omittedReserve
	}
	var b strings.Builder
	var omitted []string
	used := 0
	for i, f := range files {
		if parts[i].head == "" {
			omitted = append(omitted, visiblePath(f.Path))
			continue
		}
		n := len(parts[i].head) + len(parts[i].body)
		if used+n > budget {
			omitted = append(omitted, f.Path)
			continue
		}
		used += n
		b.WriteString(parts[i].head + parts[i].body)
	}
	if len(omitted) > 0 {
		b.WriteString(omittedMark + "\n")
		room, listed := max-used-omittedReserve, 0
		for _, p := range omitted {
			if len(p)+1 > room {
				break
			}
			b.WriteString(p + "\n")
			room -= len(p) + 1
			listed++
		}
		if listed < len(omitted) {
			b.WriteString(moreLine(len(omitted) - listed))
		}
	}
	return b.String(), omitted
}

// SectionsOf finds each file's text in a digest. Only a header whose path and hash prefix are
// both in valid counts; any other line that merely looks like one is file text.
func SectionsOf(text string, valid map[string]string) []Section {
	lines := strings.Split(text, "\n")
	n := len(lines)
	if n > 0 && lines[n-1] == "" {
		n--
	}
	var secs []Section
	cur, end := -1, n
	for i := 0; i < n; i++ {
		l := lines[i]
		if l == omittedMark {
			end = i
			break
		}
		if strings.HasPrefix(l, headerPrefix) && strings.HasSuffix(l, headerSuffix) {
			inner := strings.TrimSuffix(strings.TrimPrefix(l, headerPrefix), headerSuffix)
			if sp := strings.LastIndex(inner, " "); sp > 0 && valid[inner[:sp]] == inner[sp+1:] {
				if cur >= 0 {
					secs[cur].End = i
				}
				secs = append(secs, Section{Path: inner[:sp], Start: i + 2})
				cur = len(secs) - 1
				continue
			}
		}
	}
	if cur >= 0 {
		secs[cur].End = end
	}
	return secs
}

// SectionAt is the section that holds a 1-based line, if any.
func SectionAt(secs []Section, line int) (Section, bool) {
	for _, s := range secs {
		if line >= s.Start && line <= s.End {
			return s, true
		}
	}
	return Section{}, false
}
