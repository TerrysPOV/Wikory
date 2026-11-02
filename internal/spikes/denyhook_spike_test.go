//go:build spike

package spikes

import (
	"regexp"
	"strings"
	"testing"
)

// humanOnly is the prototype matcher for the 1d PreToolUse deny hook.
var humanOnly = regexp.MustCompile(`(^|[\s;&|()'"` + "`" + `/])wikory(\.exe)?['"]?\s+(trust|backup)\b`)

func denies(cmd string) bool {
	collapsed := strings.NewReplacer(`\`+"\n", " ", "''", "", `""`, "").Replace(cmd)
	return humanOnly.MatchString(collapsed)
}

func TestDenyHookCorpus(t *testing.T) {
	mustCatch := []string{
		"wikory trust",
		"wikory trust --grant team",
		"/usr/local/bin/wikory backup",
		"env FOO=1 wikory trust",
		`bash -c "wikory trust"`,
		"cd /x && wikory trust",
		"wikory.exe trust",
		`"wikory" trust`,
		"wik''ory trust",
		"wikory \\\ntrust",
	}
	for _, c := range mustCatch {
		if !denies(c) {
			t.Errorf("prototype misses a direct form it must catch: %q", c)
		}
	}
	knownMisses := []string{
		"w=wikory; $w trust",
		"$(printf 'wik%sory' '') trust",
		"echo d2lrb3J5IHRydXN0 | base64 -d | sh",
		"cp \"$(command -v wikory)\" /tmp/x && /tmp/x trust",
		"python3 -c \"import os; os.system('wik'+'ory trust')\"",
		"printf 'wikory\\x20trust' | xargs -0 sh -c",
	}
	missed := 0
	for _, c := range knownMisses {
		if !denies(c) {
			missed++
			t.Logf("V24 cannot catch (expected): %q", c)
		}
	}
	t.Logf("V24: catches %d direct forms, misses %d of %d indirect forms", len(mustCatch), missed, len(knownMisses))
	if denies("echo wikorytrust") || denies("grep -r 'trust' wikory-notes.md") {
		t.Error("prototype over-matches ordinary commands")
	}
}
