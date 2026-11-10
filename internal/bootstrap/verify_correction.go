package bootstrap

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MinCorrectionSimilarity is how much of a corrected claim must be the original: the share of
// words the two have in common, counted with repeats, over the longer of the two. A correction
// is an edit of the claim it contradicts, not a new paragraph.
const MinCorrectionSimilarity = 0.5

// wordTrim is what is stripped from both ends of a word before it is compared or classified.
const wordTrim = ".,;:!?()[]{}\"'`"

var (
	// unitWord is a number with a short unit or ordinal (30s, 5MB, 10ms, 3rd): a value, not a name.
	unitWord = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?[A-Za-z]{1,3}$`)
	// abbreviation is e.g, i.e, a.m and the like: single letters joined by dots.
	abbreviation = regexp.MustCompile(`^([A-Za-z]\.)+[A-Za-z]?$`)
	// decimal is a plain number such as 2.5 or 1.2.3.
	decimal = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
)

// checkCorrection holds the corrected claim of a contradicted verdict to the code it cites. The
// quote must appear verbatim (canonicalised, as an extract excerpt is checked) inside one cited
// range and be at least MinExcerptRunes long; the correction must be a bounded edit of the
// original claim; and every path, URL or identifier-shaped word in it must appear in the cited
// lines. cited holds the text of each cited range. A refusal never repeats the verifier's words.
func checkCorrection(original, corrected, quote string, cited []string) (code, why string) {
	q := strings.Trim(canonical(quote), "\n")
	if utf8.RuneCountInString(q) < MinExcerptRunes {
		return "quote-missing", "a contradicted verdict needs a quote of at least " + strconv.Itoa(MinExcerptRunes) + " characters copied from inside one cited line range"
	}
	found := false
	for _, c := range cited {
		found = found || strings.Contains(c, q)
	}
	if !found {
		return "quote-not-in-evidence", "the quote does not appear verbatim inside any one cited line range"
	}
	if wordSimilarity(original, corrected) < MinCorrectionSimilarity {
		return "corrected-drift", "the corrected claim is not a bounded edit of the claim it contradicts: fewer than half of its words are the original's"
	}
	all := strings.Join(cited, "\n")
	for i, w := range strings.Fields(corrected) {
		w = strings.Trim(w, wordTrim)
		if nameLike(w) && !strings.Contains(all, w) {
			return "corrected-unsupported-name", "word " + strconv.Itoa(i+1) + " of the corrected claim is a path, URL or identifier that the cited lines do not contain"
		}
	}
	return "", ""
}

// words is a text's words, lower-cased and trimmed of surrounding punctuation.
func words(s string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if w = strings.Trim(w, wordTrim); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// wordSimilarity is the share of words two texts have in common, counted with repeats, over the
// word count of the longer text. It is 0 when either has no words.
func wordSimilarity(a, b string) float64 {
	wa, wb := words(a), words(b)
	longer := max(len(wa), len(wb))
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	count := map[string]int{}
	for _, w := range wa {
		count[w]++
	}
	common := 0
	for _, w := range wb {
		if count[w] > 0 {
			count[w]--
			common++
		}
	}
	return float64(common) / float64(longer)
}

// nameLike reports a word shaped like a path, a URL or a code identifier: a separator a sentence
// does not use (slash, backslash, underscore, ::, brackets, =, $, #, @; a URL has a slash, a bare
// host a dot), a dot between two
// characters (not an abbreviation such as e.g), a letter beside a digit (not a value with a unit
// such as 30s), or a change of case inside the word (camelCase, NewRuns, JSONDecoder). A
// decimal number and a plural acronym (IDs) are words, not names.
func nameLike(w string) bool {
	switch {
	case w == "":
		return false
	case strings.ContainsAny(w, "/\\_(){}[]<>=$#@") || strings.Contains(w, "::"):
		return true
	case strings.Contains(strings.Trim(w, "."), ".") && !abbreviation.MatchString(w) && !decimal.MatchString(w):
		return true
	}
	letter, digit := false, false
	rs := []rune(w)
	for i, r := range rs {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsDigit(r)
		if i > 0 && unicode.IsLower(rs[i-1]) && unicode.IsUpper(r) {
			return true
		}
		// An acronym run into a word (JSONDecoder), not a plural acronym (IDs, URLs).
		if i > 1 && i+1 < len(rs) && unicode.IsUpper(rs[i-2]) && unicode.IsUpper(rs[i-1]) && unicode.IsLower(r) && unicode.IsLower(rs[i+1]) {
			return true
		}
	}
	return letter && digit && !unitWord.MatchString(w)
}
