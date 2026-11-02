package app

import (
	"path/filepath"
	"testing"
)

// On Windows HOME is usually unset and USERPROFILE names the profile that holds .claude.
func TestTranscriptRootsFallBackToUserProfile(t *testing.T) {
	env := &Env{Getenv: func(k string) string {
		if k == "USERPROFILE" {
			return "profile"
		}
		return ""
	}}
	got := transcriptRoots(env)
	want := filepath.Join("profile", ".claude", "projects")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("transcriptRoots = %v, want [%s]", got, want)
	}
	env.Getenv = func(k string) string {
		switch k {
		case "HOME":
			return "home"
		case "USERPROFILE":
			return "profile"
		}
		return ""
	}
	if got := transcriptRoots(env); len(got) != 1 || got[0] != filepath.Join("home", ".claude", "projects") {
		t.Fatalf("HOME must win over USERPROFILE, got %v", got)
	}
}
