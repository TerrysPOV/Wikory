package confine

import (
	"fmt"
	"strings"
	"testing"
)

// The table runs on every platform: the predicate has no build tag, and Windows is the only
// platform that applies it. It names all 22 reserved device names, the ways they are written
// (case, extension), trailing dots and spaces, and alternate data stream colon forms.
func TestWindowsUnsafeNameTable(t *testing.T) {
	reserved := []string{"CON", "PRN", "AUX", "NUL"}
	for i := 1; i <= 9; i++ {
		reserved = append(reserved, fmt.Sprintf("COM%d", i), fmt.Sprintf("LPT%d", i))
	}
	if len(reserved) != 22 {
		t.Fatalf("test premise broken: %d reserved names, want 22", len(reserved))
	}
	for _, n := range reserved {
		for _, form := range []string{n, strings.ToLower(n), n + ".txt", strings.ToLower(n) + ".tar.gz"} {
			if !windowsUnsafeName(form) {
				t.Errorf("%q (a reserved device name) was accepted", form)
			}
		}
	}
	for _, n := range []string{"trailing.", "trailing ", "a.b.", "a.b ", "name..", "name  "} {
		if !windowsUnsafeName(n) {
			t.Errorf("%q (trailing dot or space) was accepted", n)
		}
	}
	for _, n := range []string{"f.txt:stream", "f.txt::$DATA", "f:", ":f", "C:", "a:b:c", "a*b", "a?b", `a"b`, "a<b", "a>b", "a|b"} {
		if !windowsUnsafeName(n) {
			t.Errorf("%q (stream or forbidden character) was accepted", n)
		}
	}
	for _, n := range []string{"COM0", "COM10", "LPT0", "LPT10", "COM", "LPT", "CONSOLE", "console.txt", "nul-x", "aux_1", "prn1", "a.con", "x.nul",
		"readme.md", ".gitignore", "a b.md", "a.b.c", "page-01.md"} {
		if windowsUnsafeName(n) {
			t.Errorf("%q (an ordinary name) was refused", n)
		}
	}
}
