package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TerrysPOV/Wikory/internal/store"
)

type initData struct {
	ProjectID string   `json:"project_id"`
	Created   bool     `json:"created"`
	Trusted   bool     `json:"trusted"`
	StoreDir  string   `json:"store_dir"`
	Checkout  string   `json:"checkout_id"`
	Written   []string `json:"files_written"`
}

func initJSON(t *testing.T, c *cli, dir string) (initData, Envelope) {
	t.Helper()
	out, errOut, code := c.run(dir, "--json", "init")
	if code != ExitOK {
		t.Fatalf("init exit %d: %s %s", code, out, errOut)
	}
	var e Envelope
	var d initData
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e.Data)
	_ = json.Unmarshal(raw, &d)
	return d, e
}

func TestInitCreatesProjectMemory(t *testing.T) {
	dir := newGitRepo(t)
	c := newCLI(t)
	d, _ := initJSON(t, c, dir)
	if !strings.HasPrefix(d.ProjectID, "wkp_") || !d.Created || !d.Trusted || len(d.Written) != 5 {
		t.Fatalf("%+v", d)
	}
	pm := filepath.Join(dir, "project-memory")
	if got := readFile(t, filepath.Join(pm, ".gitattributes")); got != "log.md merge=union\n** text eol=lf\n" {
		t.Fatalf(".gitattributes=%q", got)
	}
	cfg, err := parseConfig([]byte(readFile(t, filepath.Join(pm, "config.toml"))))
	if err != nil || cfg.ProjectID != d.ProjectID || cfg.SchemaVersion != 1 || cfg.Retrieval.BudgetTokens != 1500 {
		t.Fatalf("%+v %v", cfg, err)
	}
	if n := strings.Count(readFile(t, filepath.Join(pm, "log.md")), "job="); n != 1 {
		t.Fatalf("log lines=%d", n)
	}
	for _, f := range []string{"schema/claims.md", "wiki/README.md"} {
		if _, err := os.Stat(filepath.Join(pm, filepath.FromSlash(f))); err != nil {
			t.Fatal(err)
		}
	}
	if fi, err := os.Stat(d.StoreDir); err != nil || !fi.IsDir() || !strings.HasPrefix(d.StoreDir, c.dataDir) {
		t.Fatalf("store %q %v", d.StoreDir, err)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	dir := newGitRepo(t)
	c := newCLI(t)
	first, _ := initJSON(t, c, dir)
	second, _ := initJSON(t, c, dir)
	if second.ProjectID != first.ProjectID || second.Created || len(second.Written) != 0 || !second.Trusted {
		t.Fatalf("second run %+v", second)
	}
	if n := strings.Count(readFile(t, filepath.Join(dir, "project-memory", "log.md")), "job="); n != 1 {
		t.Fatalf("a second init appended a log line: %d", n)
	}
}

func TestInitInExistingProjectMemoryDoesNotGrantTrust(t *testing.T) {
	dir := newGitRepo(t)
	pm := filepath.Join(dir, "project-memory")
	_ = os.MkdirAll(pm, 0o755)
	cfg := renderConfig(ProjectConfig{ProjectID: "wkp_01J8Q4ZK8N3X2T5V6W7Y8Z9A0B", SchemaVersion: 1})
	_ = os.WriteFile(filepath.Join(pm, "config.toml"), cfg, 0o644)
	d, e := initJSON(t, newCLI(t), dir)
	if d.Created || d.Trusted || d.ProjectID != "wkp_01J8Q4ZK8N3X2T5V6W7Y8Z9A0B" || !strings.Contains(strings.Join(e.Notices, " "), "not trusted") {
		t.Fatalf("%+v notices=%v", d, e.Notices)
	}
}

func TestInitOutsideGitFails(t *testing.T) {
	_, errOut, code := newCLI(t).run(t.TempDir(), "init")
	if code != ExitUsage || !strings.Contains(errOut, "not inside a git repository") {
		t.Fatalf("code=%d %s", code, errOut)
	}
}

func TestConfigRejectsUnknownKeysAndBadIDs(t *testing.T) {
	for _, s := range []string{
		"project_id = \"wkp_01J8Q4ZK8N3X2T5V6W7Y8Z9A0B\"\nschema_version = 1\nheadless = true\n",
		"project_id = \"nope\"\nschema_version = 1\n",
		"project_id = \"wkp_01J8Q4ZK8N3X2T5V6W7Y8Z9A0B\"\nschema_version = 2\n",
	} {
		if _, err := parseConfig([]byte(s)); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

func TestInitWithPreexistingWikiPageDoesNotGrantTrust(t *testing.T) {
	dir := newGitRepo(t)
	wiki := filepath.Join(dir, "project-memory", "wiki")
	if err := os.MkdirAll(wiki, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wiki, "planted.md"), []byte("# planted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, e := initJSON(t, newCLI(t), dir)
	if !d.Created || d.Trusted || !strings.Contains(strings.Join(e.Notices, " "), "not trusted") {
		t.Fatalf("%+v notices=%v", d, e.Notices)
	}
	if _, err := os.Stat(filepath.Join(dir, "project-memory", "config.toml")); err != nil {
		t.Fatalf("config.toml must still be written: %v", err)
	}
}

func TestInitWithEmptyProjectMemoryDirGrantsTrust(t *testing.T) {
	dir := newGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, "project-memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, _ := initJSON(t, newCLI(t), dir)
	if !d.Created || !d.Trusted {
		t.Fatalf("%+v", d)
	}
}

func TestInitWithLoneDotfileInProjectMemoryDoesNotGrantTrust(t *testing.T) {
	dir := newGitRepo(t)
	pm := filepath.Join(dir, "project-memory")
	if err := os.MkdirAll(pm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pm, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, e := initJSON(t, newCLI(t), dir)
	if !d.Created || d.Trusted || !strings.Contains(strings.Join(e.Notices, " "), "not trusted") {
		t.Fatalf("%+v notices=%v", d, e.Notices)
	}
}

// A trust decision already recorded for this clone is never changed by init, even when init
// creates the config in an empty project-memory/ (the case that would otherwise be "trusted").
func TestInitKeepsRecordedTrustDecision(t *testing.T) {
	const fixedID = "wkp_01J8Q4ZK8N3X2T5V6W7Y8Z9A0C"
	old := newProjectID
	newProjectID = func() string { return fixedID }
	t.Cleanup(func() { newProjectID = old })

	dir := newGitRepo(t)
	c := newCLI(t)
	env := &Env{Getenv: func(k string) string {
		if k == "WIKORY_DATA_DIR" {
			return c.dataDir
		}
		return os.Getenv(k)
	}, Cwd: dir}
	repo, root, err := discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	id := identityFor(context.Background(), repo, fixedID)
	st, err := store.Resolve(context.Background(), c.dataDir, id, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordTrust(context.Background(), repo.CommonDir, store.TrustUndecided, "seeded"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	d, e := initJSON(t, c, dir)
	if !d.Created || d.Trusted || !strings.Contains(strings.Join(e.Notices, " "), "unchanged") {
		t.Fatalf("%+v notices=%v", d, e.Notices)
	}
	st, err = store.Resolve(context.Background(), c.dataDir, id, false)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, ok, err := st.TrustDecision(context.Background(), repo.CommonDir)
	if err != nil || !ok || got != store.TrustUndecided {
		t.Fatalf("decision=%q recorded=%v err=%v", got, ok, err)
	}
}
