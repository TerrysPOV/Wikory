package store

import "testing"

func TestCanonicalOrigin(t *testing.T) {
	cred := "someone:" + "x" + "@" // built at runtime: no credential-shaped literal in source
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"https://github.com/TerrysPOV/Wikory.git", "github.com/terryspov/wikory", true},
		{"git@github.com:TerrysPOV/Wikory.git", "github.com/terryspov/wikory", true},
		{"ssh://git@github.com:22/TerrysPOV/Wikory", "github.com/terryspov/wikory", true},
		{"https://" + cred + "GitHub.com/TerrysPOV/Wikory/", "github.com/terryspov/wikory", true},
		{"github.com:TerrysPOV/Wikory", "github.com/terryspov/wikory", true},
		{"/srv/git/wikory.git", "", false},
		{`C:\repos\wikory`, "", false},
		{"file:///srv/git/wikory.git", "", false},
		{"https://github.com/onlyowner", "", false},
		{"https://github.com/a/../b", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := CanonicalOrigin(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got (%q,%v) want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestStoreKey(t *testing.T) {
	a := StoreKey("wkp_A", "github.com/o/r", "/x/.git")
	if a != StoreKey("wkp_A", "github.com/o/r", "/elsewhere/.git") {
		t.Fatal("with an origin, the key must not depend on the path")
	}
	if a == StoreKey("wkp_A", "github.com/fork/r", "/x/.git") {
		t.Fatal("a fork with the same project id must get a separate store")
	}
	if StoreKey("wkp_A", "", "/x/.git") == StoreKey("wkp_A", "", "/y/.git") {
		t.Fatal("without an origin, different repositories must get different keys")
	}
}
