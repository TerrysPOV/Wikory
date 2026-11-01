package claim

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const specExample = `<!-- claim c=01J8Q4ZK8N3X2T5V6W7Y8Z9A0B kind=current-impl status=verified vrev=a1b2c3d scope=repo ev=src/queue.ts@9f3e1a2 dep=src/queue.ts#enqueue th=7c1e2f aka="retry;replay" -->`

func TestParseSpecExample(t *testing.T) {
	c, err := ParseComment(specExample)
	if err != nil {
		t.Fatal(err)
	}
	want := Claim{ID: "01J8Q4ZK8N3X2T5V6W7Y8Z9A0B", Kind: "current-impl", Status: "verified", VRev: "a1b2c3d", Scope: "repo",
		Evidence: []Evidence{{Kind: EvBlob, Path: "src/queue.ts", OID: "9f3e1a2"}},
		Deps:     []string{"src/queue.ts#enqueue"}, TH: "7c1e2f", AKA: []string{"retry", "replay"}}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got %+v\nwant %+v", c, want)
	}
	if got := c.DepPaths(); !reflect.DeepEqual(got, []string{"src/queue.ts"}) {
		t.Fatalf("dep paths %v", got)
	}
}

func TestRoundTripHardValues(t *testing.T) {
	c := Claim{ID: "01J8Q4ZK8N3X2T5V6W7Y8Z9A0B", Kind: "rationale", Status: "unverified", Scope: "repo",
		Evidence: []Evidence{
			{Kind: EvBlob, Path: "docs/my file;v2@x#y--z%20.md", OID: "0123456789abcdef0123456789abcdef01234567", Algo: "sha1"},
			{Kind: EvBlob, Path: "src/short.go", OID: "abcdef1234567"},
			{Kind: EvCommit, Ref: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Algo: "sha256"},
			{Kind: EvTurn, Ref: "turn-1"},
			{Kind: EvExternal, Ref: "https://example.invalid/a=b"},
		},
		Deps:   []string{"src/a b.go#Do--It", "src/plain.go"},
		AKA:    []string{"retry job", "re-play"},
		TH:     "0123456789ab",
		Author: "human",
	}
	line := c.Render()
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "<!-- claim "), " -->")
	if strings.Contains(inner, "--") {
		t.Fatalf("rendered body contains --: %s", line)
	}
	got, err := ParseComment(line)
	if err != nil {
		t.Fatalf("%v\n%s", err, line)
	}
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip\n got %+v\nwant %+v\nline %s", got, c, line)
	}
}

func TestMalformed(t *testing.T) {
	cases := map[string]string{
		"duplicate key":       `<!-- claim kind=intent status=unverified kind=rationale -->`,
		"unknown key":         `<!-- claim kind=intent status=unverified colour=red -->`,
		"double hyphen":       `<!-- claim kind=intent status=unverified dep=a--b.go -->`,
		"unterminated quote":  `<!-- claim kind=intent status=unverified aka="x -->`,
		"bad kind":            `<!-- claim kind=guess status=unverified -->`,
		"missing status":      `<!-- claim kind=intent -->`,
		"backslash path":      `<!-- claim kind=intent status=unverified ev=src\q.go@abcdef1 -->`,
		"absolute path":       `<!-- claim kind=intent status=unverified dep=/etc/passwd -->`,
		"parent path":         `<!-- claim kind=intent status=unverified dep=../x.go -->`,
		"bad oid":             `<!-- claim kind=intent status=unverified ev=a.go@xyz1234 -->`,
		"short full form":     `<!-- claim kind=intent status=unverified ev=a.go@sha1:abcdef1 -->`,
		"unknown algorithm":   `<!-- claim kind=intent status=unverified ev=a.go@md5:0123456789abcdef0123456789abcdef -->`,
		"sha256 length":       `<!-- claim kind=intent status=unverified ev=commit:sha256:0123456789abcdef0123456789abcdef01234567 -->`,
		"empty list item":     `<!-- claim kind=intent status=unverified dep=a.go;;b.go -->`,
		"bad ulid":            `<!-- claim c=notaulid kind=intent status=unverified -->`,
		"not a claim comment": `<!-- note kind=intent status=unverified -->`,
		"bare gt":             `<!-- claim kind=intent status=unverified dep=a>b.go -->`,
		"scope not repo":      `<!-- claim kind=intent status=unverified scope=branch -->`,
	}
	for name, line := range cases {
		if _, err := ParseComment(line); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: want ErrMalformed, got %v", name, err)
		}
	}
}

func TestCanonicalTextHash(t *testing.T) {
	base := TextHash("Jobs are acknowledged after the handler returns.\nSo a crash replays the job.")
	same := []string{
		"Jobs are acknowledged after the handler returns.\r\nSo a crash replays the job.\r\n",
		"Jobs are acknowledged after the handler returns.   \nSo a crash replays the job.\t",
		"\n\nJobs are acknowledged after the handler returns.\nSo a crash replays the job.\n\n",
	}
	for _, s := range same {
		if TextHash(s) != base {
			t.Errorf("should hash equal: %q", s)
		}
	}
	if TextHash("café") != TextHash("café") {
		t.Error("NFC and NFD forms must hash equal")
	}
	if TextHash("Jobs are acknowledged before the handler returns.") == base {
		t.Error("a real edit must change the hash")
	}
	if len(base) != 12 {
		t.Errorf("th length %d", len(base))
	}
}

// nastyItems are item contents that touch the grammar or the shell-ish punctuation around it.
// Each must survive Render then ParseComment unchanged.
var nastyItems = []string{
	",", "(", ")", "&", "'", "$", "!", "*", "[", "]", "{", "}", "^", "`", "|", "?", "\\", "\t",
	"<", ">", `"`, "=", ";", "@", "#", " ", "%", "~", "+", ":", "-", "--", "---", "é", "\u00a0",
	"%2C", "%28", "%29", "%26", "%27", "%24", "%21", "%2A", "%5B", "%5D", "%25", "%2525", "%",
}

// pathSafe drops the items that are not legal inside a repository path on every platform
// (separators, Windows-reserved characters), because paths are checked by confine.
func pathSafe(s string) bool { return !strings.ContainsAny(s, "\\<>\"|?*:\t") && s != "\u00a0" }

func TestRenderParseRoundTripNastyCharacters(t *testing.T) {
	for _, s := range nastyItems {
		c := Claim{ID: "01J8Q4ZK8N3X2T5V6W7Y8Z9A0B", Kind: "rationale", Status: "unverified", Scope: "repo",
			Evidence: []Evidence{
				{Kind: EvTurn, Ref: "t" + s + "u"},
				{Kind: EvExternal, Ref: "https://example.invalid/a" + s + "b"},
			},
			Deps:        []string{"src/a.go#sym" + s + "x"},
			AKA:         []string{"alias" + s + "x", s + "lead"},
			Supersedes:  []string{"old" + s + "x"},
			Contradicts: []string{"other" + s + "x"},
		}
		if pathSafe(s) {
			c.Evidence = append(c.Evidence, Evidence{Kind: EvBlob, Path: "docs/a" + s + "b.md"})
			c.Deps = append(c.Deps, "docs/d"+s+"e.go")
		}
		line := c.Render()
		got, err := ParseComment(line)
		if err != nil {
			t.Errorf("%q: rendered line does not parse: %v\n%s", s, err, line)
			continue
		}
		if !reflect.DeepEqual(got, c) {
			t.Errorf("%q: round trip changed the claim\n got %+v\nwant %+v\nline %s", s, got, c, line)
		}
	}
}

// The review's case: a percent-encoded comma in a path parses, and rendering used to emit a
// raw comma, which the parser rejects. Every percent form below must come back as itself.
func TestParsedPercentFormsRenderBack(t *testing.T) {
	for _, enc := range []string{"%2C", "%28", "%29", "%26", "%27", "%24", "%21", "%2A", "%5B", "%5D", "%7B", "%7D", "%5E", "%60", "%7C", "%3F", "%25", "%2525"} {
		line := "<!-- claim kind=intent status=unverified ev=docs/a" + enc + "b.md dep=docs/a" + enc + "b.md#s" + enc + " sup=s" + enc + " -->"
		c, err := ParseComment(line)
		if err != nil {
			t.Errorf("%s: hand-written line does not parse: %v", enc, err)
			continue
		}
		again, err := ParseComment(c.Render())
		if err != nil {
			t.Errorf("%s: a valid claim rendered to a line the parser rejects: %v\n%s", enc, err, c.Render())
			continue
		}
		if !reflect.DeepEqual(again, c) {
			t.Errorf("%s: round trip changed the claim\n got %+v\nwant %+v", enc, again, c)
		}
	}
}

// Whatever the contents, a rendered value outside quotes uses only the bare alphabet.
func TestRenderedValuesStayInsideTheBareAlphabet(t *testing.T) {
	c := Claim{Kind: "intent", Status: "unverified", Supersedes: []string{"a,b(c)&d'e$f!g*h[i]j"}, Contradicts: []string{"x y\tz"}}
	line := c.Render()
	body := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
	for _, f := range strings.Fields(body) {
		_, v, _ := strings.Cut(f, "=")
		if !strings.HasPrefix(v, `"`) && !bareRe.MatchString(v) {
			t.Errorf("value %q is outside the bare alphabet in %s", v, line)
		}
	}
}
