// Package claim parses and renders the one-line claim metadata comment (spec 5.3) and
// parses wiki pages into claim blocks (spec 5.4).
package claim

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/ids"
)

var ErrMalformed = errors.New("claim: malformed metadata comment")

func malformed(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformed, fmt.Sprintf(format, a...))
}

// Evidence kinds.
const (
	EvBlob     = "blob"
	EvCommit   = "commit"
	EvTurn     = "turn"
	EvExternal = "ext"
)

// Evidence is one evidence reference. Blob evidence has Path and, once stamped, the full
// OID with its algorithm (Algo "sha1" or "sha256"). A hand-written short OID parses with an
// empty Algo: it is unresolved until wikory page stamp resolves it to a unique full id.
// Commit evidence keeps its id in Ref, with Algo set once resolved.
type Evidence struct {
	Kind string
	Path string
	OID  string
	Algo string
	Ref  string
}

// oidLen is the full hex length for each supported object format.
var oidLen = map[string]int{"sha1": 40, "sha256": 64}

// parseOID accepts "<algo>:<full hex>" (resolved) or bare hex of 7 to 64 characters
// (an unresolved short form, Algo empty).
func parseOID(v string) (algo, oid string, err error) {
	if a, h, ok := strings.Cut(v, ":"); ok {
		n, known := oidLen[a]
		if !known || len(h) != n || !hexRe.MatchString(h) {
			return "", "", malformed("object id %q is not <sha1|sha256>:<full hex>", v)
		}
		return a, h, nil
	}
	if !hexRe.MatchString(v) {
		return "", "", malformed("object id %q is not hex", v)
	}
	return "", v, nil
}

func renderOID(algo, oid string) string {
	if algo == "" {
		return oid
	}
	return algo + ":" + oid
}

// Claim is the parsed metadata of one claim block.
type Claim struct {
	ID          string
	Kind        string
	Status      string
	VRev        string
	Scope       string
	TH          string
	Author      string
	Evidence    []Evidence
	Deps        []string
	AKA         []string
	Supersedes  []string
	Contradicts []string
}

var (
	kinds    = map[string]bool{"current-impl": true, "intent": true, "rationale": true, "hypothesis": true}
	statuses = map[string]bool{"verified": true, "unverified": true, "conflicting": true, "historical": true, "superseded": true}
	hexRe    = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	thRe     = regexp.MustCompile(`^[0-9a-f]{6,64}$`)
	bareRe   = regexp.MustCompile(`^[A-Za-z0-9._/@#:;%~+\-]+$`)
)

const (
	prefix = "<!-- claim "
	suffix = " -->"
)

// IsClaimComment reports whether a line is meant to be a claim comment.
func IsClaimComment(line string) bool {
	s := strings.TrimSpace(line)
	return strings.HasPrefix(s, prefix) || s == "<!-- claim -->"
}

// ParseComment parses one claim metadata line.
func ParseComment(line string) (Claim, error) {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, prefix) || !strings.HasSuffix(s, suffix) || len(s) < len(prefix)+len(suffix) {
		return Claim{}, malformed("not a claim comment")
	}
	body := s[len(prefix) : len(s)-len(suffix)]
	if strings.Contains(body, "--") {
		return Claim{}, malformed("the comment body contains --")
	}
	fields, err := splitFields(body)
	if err != nil {
		return Claim{}, err
	}
	var c Claim
	seen := map[string]bool{}
	for _, f := range fields {
		k, raw, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return Claim{}, malformed("field %q has no key=value", f)
		}
		if seen[k] {
			return Claim{}, malformed("duplicate field %q", k)
		}
		seen[k] = true
		v, err := unquote(raw)
		if err != nil {
			return Claim{}, err
		}
		if err := c.set(k, v); err != nil {
			return Claim{}, err
		}
	}
	if c.Kind == "" || c.Status == "" {
		return Claim{}, malformed("kind and status are required")
	}
	return c, nil
}

func splitFields(body string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, r := range body {
		switch {
		case r == '"':
			inQuote = !inQuote
			cur.WriteRune(r)
		case r == ' ' && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if inQuote {
		return nil, malformed("unterminated quote")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
}

func unquote(v string) (string, error) {
	if strings.HasPrefix(v, `"`) {
		if len(v) < 2 || !strings.HasSuffix(v, `"`) {
			return "", malformed("bad quoting in %q", v)
		}
		inner := v[1 : len(v)-1]
		if strings.ContainsAny(inner, `">`) {
			return "", malformed("quoted value contains a quote or >")
		}
		return inner, nil
	}
	if !bareRe.MatchString(v) {
		return "", malformed("value %q has characters outside the bare alphabet", v)
	}
	return v, nil
}

func splitList(v string) []string { return strings.Split(v, ";") }

func checkPath(p string) error {
	if _, err := confine.Split(p); err != nil {
		return malformed("path %q is not a clean repository-relative path", p)
	}
	return nil
}

func (c *Claim) set(k, v string) error {
	switch k {
	case "c":
		if !ids.IsULID(v) {
			return malformed("c=%q is not a ULID", v)
		}
		c.ID = v
	case "kind":
		if !kinds[v] {
			return malformed("unknown kind %q", v)
		}
		c.Kind = v
	case "status":
		if !statuses[v] {
			return malformed("unknown status %q", v)
		}
		c.Status = v
	case "vrev":
		if !hexRe.MatchString(v) {
			return malformed("vrev %q is not a revision", v)
		}
		c.VRev = v
	case "scope":
		if v != "repo" {
			return malformed("committed scope must be repo, got %q", v)
		}
		c.Scope = v
	case "th":
		if !thRe.MatchString(v) {
			return malformed("th %q is not a hash", v)
		}
		c.TH = v
	case "author":
		if v != "agent" && v != "human" {
			return malformed("author must be agent or human, got %q", v)
		}
		c.Author = v
	case "ev":
		for _, raw := range splitList(v) {
			e, err := parseEvidence(raw)
			if err != nil {
				return err
			}
			c.Evidence = append(c.Evidence, e)
		}
	case "dep":
		for _, raw := range splitList(v) {
			d, err := parseDep(raw)
			if err != nil {
				return err
			}
			c.Deps = append(c.Deps, d)
		}
	case "aka", "sup", "contra":
		var items []string
		for _, raw := range splitList(v) {
			it, err := decodeItem(raw)
			if err != nil {
				return err
			}
			items = append(items, it)
		}
		switch k {
		case "aka":
			c.AKA = items
		case "sup":
			c.Supersedes = items
		default:
			c.Contradicts = items
		}
	default:
		return malformed("unknown field %q", k)
	}
	return nil
}

func parseEvidence(raw string) (Evidence, error) {
	for _, k := range []string{EvCommit, EvTurn, EvExternal} {
		if rest, ok := strings.CutPrefix(raw, k+":"); ok {
			if k == EvCommit {
				algo, oid, err := parseOID(rest)
				if err != nil {
					return Evidence{}, err
				}
				return Evidence{Kind: k, Ref: oid, Algo: algo}, nil
			}
			ref, err := decodeItem(rest)
			if err != nil {
				return Evidence{}, err
			}
			return Evidence{Kind: k, Ref: ref}, nil
		}
	}
	pathPart, oid, hasOID := strings.Cut(raw, "@")
	p, err := decodeItem(pathPart)
	if err != nil {
		return Evidence{}, err
	}
	if err := checkPath(p); err != nil {
		return Evidence{}, err
	}
	if !hasOID {
		return Evidence{Kind: EvBlob, Path: p}, nil
	}
	algo, full, err := parseOID(oid)
	if err != nil {
		return Evidence{}, err
	}
	return Evidence{Kind: EvBlob, Path: p, OID: full, Algo: algo}, nil
}

func parseDep(raw string) (string, error) {
	pathPart, sym, hasSym := strings.Cut(raw, "#")
	p, err := decodeItem(pathPart)
	if err != nil {
		return "", err
	}
	if err := checkPath(p); err != nil {
		return "", err
	}
	if !hasSym {
		return p, nil
	}
	s, err := decodeItem(sym)
	if err != nil {
		return "", err
	}
	return p + "#" + s, nil
}

// DepPaths returns dependency paths without any #symbol.
func (c Claim) DepPaths() []string {
	out := make([]string, len(c.Deps))
	for i, d := range c.Deps {
		out[i], _, _ = strings.Cut(d, "#")
	}
	return out
}

// Render produces the canonical one-line metadata comment.
func (c Claim) Render() string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	add("c", c.ID)
	add("kind", c.Kind)
	add("status", c.Status)
	add("vrev", c.VRev)
	add("scope", c.Scope)
	var evs []string
	for _, e := range c.Evidence {
		switch e.Kind {
		case EvBlob:
			item := encodeItem(e.Path)
			if e.OID != "" {
				item += "@" + renderOID(e.Algo, e.OID)
			}
			evs = append(evs, item)
		case EvCommit:
			evs = append(evs, EvCommit+":"+renderOID(e.Algo, e.Ref))
		default:
			evs = append(evs, e.Kind+":"+encodeItem(e.Ref))
		}
	}
	add("ev", strings.Join(evs, ";"))
	var deps []string
	for _, d := range c.Deps {
		p, sym, hasSym := strings.Cut(d, "#")
		item := encodeItem(p)
		if hasSym {
			item += "#" + encodeItem(sym)
		}
		deps = append(deps, item)
	}
	add("dep", strings.Join(deps, ";"))
	add("th", c.TH)
	if len(c.AKA) > 0 {
		parts = append(parts, `aka="`+joinEncoded(c.AKA)+`"`)
	}
	add("author", c.Author)
	add("sup", joinEncoded(c.Supersedes))
	add("contra", joinEncoded(c.Contradicts))
	return prefix + strings.Join(parts, " ") + suffix
}

// CanonicalText: LF line endings, Unicode NFC, trailing whitespace trimmed per line,
// leading and trailing blank lines removed (spec 5.3).
func CanonicalText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = norm.NFC.String(s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// TextHash is the th value: the first 12 hex characters of SHA-256 over the canonical text.
func TextHash(s string) string { return ids.SHA256Hex([]byte(CanonicalText(s)))[:12] }
