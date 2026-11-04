package bootstrap

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/redact"
)

const (
	MaxClaimBytes    = 600
	MinExcerptRunes  = 12
	MaxClaimsPerItem = 40
)

// ProposedClaim is one claim a subagent proposes from a work file. It has no status, no id
// and no evidence field: the binary decides those.
type ProposedClaim struct {
	Text    string `json:"text"`
	Kind    string `json:"kind"`
	Excerpt string `json:"excerpt"`
	Locator string `json:"locator"`
}

// Proposal is a subagent's answer for one extract item.
type Proposal struct {
	Item   string          `json:"item"`
	Epoch  int             `json:"epoch"`
	Claims []ProposedClaim `json:"claims"`
	Usage  Usage           `json:"usage"`
}

// Reject says why one proposed claim was refused. Index is its position in the proposal.
type Reject struct {
	Index int    `json:"index"`
	Code  string `json:"code"`
	Why   string `json:"why"`
}

// Accepted is a proposed claim that passed the checks, with its original position.
type Accepted struct {
	Index int
	ProposedClaim
}

var (
	validKinds = map[string]bool{"current-impl": true, "intent": true, "rationale": true, "hypothesis": true}
	locatorRe  = regexp.MustCompile(`^L([1-9][0-9]{0,6})-L([1-9][0-9]{0,6})$`)
	// badLead matches text a renderer would not show as a plain paragraph.
	badLead  = regexp.MustCompile("^\\s{0,3}([#>*+|-]|[0-9]+[.)]|```|~~~|<)")
	inlineHT = regexp.MustCompile(`<[A-Za-z/!?]`)
	// underscoreRule is a thematic break written with underscores, which badLead does not cover.
	// CheckText has already refused leading space, so no indent is allowed for.
	underscoreRule = regexp.MustCompile(`^(_[ \t]*){3,}$`)
)

// startsLinkDefinition reports text that could be read as a link reference definition, which a
// renderer swallows instead of showing. CommonMark's label rules (escaped brackets, nesting) are
// too fiddly to follow exactly, so any text that opens with "[" and has "]:" anywhere after it
// is refused. Ordinary prose that merely opens with a bracket has no "]:" and passes.
func startsLinkDefinition(t string) bool {
	return strings.HasPrefix(t, "[") && strings.Contains(t, "]:")
}

// CheckText is the shared rule for claim text, used for extracted claims and for the corrected
// claim of a contradicting verdict. It returns an empty code when the text is acceptable.
func CheckText(t string) (code, why string) {
	switch {
	case strings.TrimSpace(t) == "":
		return "bad-text", "the text is empty"
	case !utf8.ValidString(t):
		return "bad-text", "the text is not valid UTF-8"
	case len(t) > MaxClaimBytes:
		return "bad-text", "the text is longer than " + strconv.Itoa(MaxClaimBytes) + " bytes"
	case len(redact.Scan(t)) > 0:
		return "secret", "the text looks like it contains a credential or key, so it was not stored"
	case strings.ContainsAny(t, "\r\n\u2028\u2029"):
		return "bad-text", "a claim is one paragraph with no line breaks"
	case t != strings.TrimSpace(t):
		return "bad-text", "the text has leading or trailing whitespace"
	case strings.Contains(t, "<!--") || strings.Contains(t, "-->"):
		return "bad-text", "the text contains comment markers"
	case badLead.MatchString(t) || underscoreRule.MatchString(t) || startsLinkDefinition(t):
		return "bad-text", "the text would render as a heading, quote, list, table, rule, link definition or code, not a paragraph"
	case inlineHT.MatchString(t):
		return "bad-text", "the text contains inline HTML, which the index would drop"
	case strings.Contains(t, "](") || strings.Contains(t, "!["):
		// Claim text is written into a page as it is, so link or image markup would render as a
		// live link or an image pointing wherever the source said.
		return "markup", "the text contains link or image markup, which a page would render as a link or an image"
	}
	for _, r := range t {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "bad-text", "the text contains a control or invisible character"
		}
	}
	return "", ""
}

// Safe makes text that may carry model-controlled content fit to print. Secret shapes are
// redacted, control characters dropped and the length bounded, so a rejection message or an
// error can never carry a credential into the working agent's transcript.
func Safe(s string) string {
	s, _ = redact.String(s)
	var b strings.Builder
	n := 0
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if n++; n > 200 {
			b.WriteString("...")
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

// canonical is the text form excerpts are compared in: LF line endings, NFC, trailing blanks
// trimmed per line. Unlike claim.CanonicalText it keeps leading and trailing blank lines, so
// line numbers still mean the work file's own lines.
func canonical(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(norm.NFC.String(s), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}

// CheckProposal keeps the claims that are well formed and grounded in the work file: the
// excerpt must appear verbatim (canonicalised) in the file, inside the lines the locator names.
// It returns the accepted claims with their original positions and a reject for every other.
func CheckProposal(p Proposal, work []byte) (accepted []Accepted, rejects []Reject) {
	canon := canonical(string(work))
	lines := strings.Split(canon, "\n")
	n := len(lines)
	if n > 0 && lines[n-1] == "" {
		n--
	}
	seen := map[string]bool{}
	for i, c := range p.Claims {
		rej := func(code, why string) { rejects = append(rejects, Reject{Index: i, Code: code, Why: why}) }
		if i >= MaxClaimsPerItem {
			rej("too-many", "an item may yield at most "+strconv.Itoa(MaxClaimsPerItem)+" claims")
			continue
		}
		if !validKinds[c.Kind] {
			rej("bad-kind", "kind must be current-impl, intent, rationale or hypothesis")
			continue
		}
		if code, why := CheckText(c.Text); code != "" {
			rej(code, why)
			continue
		}
		key := claim.CanonicalText(c.Text)
		if seen[key] {
			rej("duplicate", "the same claim text was proposed twice")
			continue
		}
		ex := strings.Trim(canonical(c.Excerpt), "\n")
		if utf8.RuneCountInString(ex) < MinExcerptRunes {
			rej("excerpt-short", "the excerpt is too short to show where the claim comes from")
			continue
		}
		if !strings.Contains(canon, ex) {
			rej("excerpt-missing", "the excerpt does not appear verbatim in the work file")
			continue
		}
		m := locatorRe.FindStringSubmatch(c.Locator)
		if m == nil {
			rej("bad-locator", "the locator must look like L12-L20 (lines of the work file)")
			continue
		}
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if a > b || b > n {
			rej("bad-locator", "the locator is backwards or beyond the end of the work file")
			continue
		}
		if !strings.Contains(strings.Join(lines[a-1:b], "\n"), ex) {
			rej("locator-mismatch", "the excerpt is in the work file but not at the lines the locator names")
			continue
		}
		seen[key] = true
		accepted = append(accepted, Accepted{Index: i, ProposedClaim: c})
	}
	return accepted, rejects
}
