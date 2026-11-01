package claim

import (
	"fmt"
	"net/url"
	"strings"
)

// encodeItem percent-encodes one list item so it survives the comment grammar. "%" is
// handled in the same pass as every other byte, so encoding is reversible.
func encodeItem(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '%':
			b.WriteString("%25")
		case ch == ' ' || ch == ';' || ch == '=' || ch == '"' || ch == '>' || ch == '<' || ch == '@' || ch == '#':
			fmt.Fprintf(&b, "%%%02X", ch)
		case ch == '-' && i > 0 && s[i-1] == '-':
			b.WriteString("%2D")
		case ch < 0x20 || ch >= 0x7f:
			fmt.Fprintf(&b, "%%%02X", ch)
		default:
			b.WriteByte(ch)
		}
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
