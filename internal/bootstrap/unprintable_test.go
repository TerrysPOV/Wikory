package bootstrap

import (
	"context"
	"strings"
	"testing"
)

// A line or paragraph separator, or a bidirectional override, in a file name is as dangerous as
// a newline: it can make a header or a report line read as something else. Such a name is a bad
// name, never read, and every printer shows it with "?" in place of the character.
func TestSeparatorsAndBidiOverridesInANameMakeItABadName(t *testing.T) {
	ctx := context.Background()
	repo := &memReader{name: "repo", files: map[string]string{
		"internal/a/x\u2028y.go": "package a\n",
		"internal/a/x\u202ey.go": "package a\n",
		"internal/a/ok.go":       "package a\n",
	}}
	res, err := Inventory(ctx, []Reader{repo}, Options{MaxFileBytes: MaxFileBytes, Budget: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"internal/a/x\u2028y.go", "internal/a/x\u202ey.go"} {
		if got := srcBy(res, p); got.Disposition != DispBadName {
			t.Errorf("%q: disposition %q, want %q", p, got.Disposition, DispBadName)
		}
	}
	for _, s := range []string{"x\u2028y", "x\u2029y", "x\u202ey", "x\ny"} {
		for name, got := range map[string]string{"visiblePath": visiblePath(s), "safeLabel": safeLabel(s), "Safe": Safe(s)} {
			if strings.ContainsAny(got, "\u2028\u2029\u202e\n") {
				t.Errorf("%s(%q) = %q still holds the character", name, s, got)
			}
		}
	}
	if code, _ := CheckText("The queue x\u202ey acks."); code != "bad-text" {
		t.Errorf("claim text with a bidi override is refused, got %q", code)
	}
}

// Safe and safeLabel bound what they print by the same cap.
func TestSafeAndSafeLabelShareOneCap(t *testing.T) {
	long := strings.Repeat("a", maxShownRunes+50)
	if a, b := Safe(long), safeLabel(long); a != b || len([]rune(a)) != maxShownRunes+3 {
		t.Fatalf("Safe gives %d runes and safeLabel %d; both must stop at %d plus an ellipsis", len([]rune(a)), len([]rune(b)), maxShownRunes)
	}
}
