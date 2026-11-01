package gitx

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// isolatedGlobalConfig is the global git config every test in this package sees. TestMain
// points git at it, so a developer's or a CI runner's own global config (the macOS runners
// define filter.lfs.* drivers) never changes what a test observes.
var isolatedGlobalConfig string

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "gitx-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitx tests: cannot create an isolated HOME:", err)
		os.Exit(2)
	}
	isolatedGlobalConfig = filepath.Join(home, "gitconfig")
	if err := os.WriteFile(isolatedGlobalConfig, nil, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "gitx tests: cannot create the isolated global config:", err)
		os.Exit(2)
	}
	for k, v := range map[string]string{
		"HOME":              home,
		"USERPROFILE":       home,
		"XDG_CONFIG_HOME":   filepath.Join(home, "xdg"),
		"GIT_CONFIG_GLOBAL": isolatedGlobalConfig,
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintln(os.Stderr, "gitx tests: cannot set", k, err)
			os.Exit(2)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
