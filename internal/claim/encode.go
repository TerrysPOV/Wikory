package claim

import (
	"fmt"
	"net/url"
	"strings"
)

// bareSafe is the part of the bare alphabet (bareRe) that encodeItem leaves as it is. The rest
// of the bare alphabet is structural (";" separates items, "@" and "#" split an evidence or
// dependency item, "%" starts an escape), so those are encoded too.
func bareSafe(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' ||
		strings.IndexByte("._/~+:-", ch) >= 0
}

// encodeItem percent-encodes one list item so it survives the comment grammar: every byte
// outside bareSafe becomes %XX, and a second "-" in a row is encoded so that a rendered body
// never holds "--". "%" is handled in the same pass as every other byte, so encoding is
// reversible, and a rendered item always stays inside the bare alphabet.
func encodeItem(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if bareSafe(ch) && !(ch == '-' && i > 0 && s[i-1] == '-') {
			b.WriteByte(ch)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", ch)
	}
	return b.String()
}

func decodeItem(s string) (string, error) {
	if s == "" {
		return "", malformed("empty list item")
	}
	v, err := url.PathUnescape(s)
	if err != nil {
		return "", malformed("bad percent-encoding in %q", s)
	}
	return v, nil
}

func joinEncoded(items []string) string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = encodeItem(it)
	}
	return strings.Join(out, ";")
}
