package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/gitx"
	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/publish"
	"github.com/TerrysPOV/Wikory/internal/store"
)

const (
	pmDir                = "project-memory"
	configPath           = pmDir + "/config.toml"
	logPath              = pmDir + "/log.md"
	gitattributesPath    = pmDir + "/.gitattributes"
	gitattributesContent = "log.md merge=union\n** text eol=lf\n"
	wikiDir              = pmDir + "/wiki"
	projectSchemaVersion = 1
	defaultBudgetTokens  = 1500
)

var (
	errNotGit         = &UsageError{Msg: "not inside a git repository (Wikory v1 needs git)"}
	errNotInitialised = errors.New("this repository has no project-memory/config.toml; run wikory init")
)

// ProjectConfig is the committed project-memory/config.toml.
type ProjectConfig struct {
	ProjectID     string `toml:"project_id"`
	SchemaVersion int    `toml:"schema_version"`
	Retrieval     struct {
		BudgetTokens int `toml:"budget_tokens"`
	} `toml:"retrieval"`
}

func parseConfig(data []byte) (ProjectConfig, error) {
	var c ProjectConfig
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, fmt.Errorf("project-memory/config.toml: %w", err)
	}
	if !strings.HasPrefix(c.ProjectID, "wkp_") || !ids.IsULID(c.ProjectID[4:]) {
		return c, fmt.Errorf("project-memory/config.toml: project_id %q is not a Wikory project id", c.ProjectID)
	}
	if c.SchemaVersion > projectSchemaVersion {
		return c, fmt.Errorf("project-memory/config.toml: schema_version %d is newer than this binary understands; upgrade wikory", c.SchemaVersion)
	}
	if c.SchemaVersion < 1 {
		return c, errors.New("project-memory/config.toml: schema_version is missing")
	}
	if c.Retrieval.BudgetTokens == 0 {
		c.Retrieval.BudgetTokens = defaultBudgetTokens
	}
	return c, nil
}

func renderConfig(c ProjectConfig) []byte {
	budget := c.Retrieval.BudgetTokens
	if budget == 0 {
		budget = defaultBudgetTokens
	}
	return []byte(fmt.Sprintf(`# Wikory project configuration. Committed and shared with the team.
# Identity is this id, not a path or a remote (spec 4.1). Do not change it.
project_id = %q
schema_version = %d

[retrieval]
# Token budget for the SessionStart briefing.
budget_tokens = %d
`, c.ProjectID, c.SchemaVersion, budget))
}

// project is what a command knows about the current checkout.
type project struct {
	Repo       *gitx.Repo
	RepoRoot   *confine.Root
	Config     ProjectConfig
	Identity   store.Identity
	Store      *store.Store
	Checkout   store.Checkout
	Registered bool
	Trusted    bool
	Notices    []string
}

func (p *project) Close() {
	if p.Store != nil {
		p.Store.Close()
	}
	if p.RepoRoot != nil {
		p.RepoRoot.Close()
	}
}

// testHookSlowDiscover lets a test make git discovery slow. Nil in production.
var testHookSlowDiscover atomic.Pointer[func()]

// discover finds the repository and opens its root without reading the config.
func discover(ctx context.Context, env *Env) (*gitx.Repo, *confine.Root, error) {
	if f := testHookSlowDiscover.Load(); f != nil {
		(*f)()
	}
	repo, err := gitx.Discover(ctx, env.Cwd)
	if err != nil {
		return nil, nil, errNotGit
	}
	root, err := confine.Open(repo.Top)
	if err != nil {
		return nil, nil, err
	}
	return repo, root, nil
}

func identityFor(ctx context.Context, repo *gitx.Repo, projectID string) store.Identity {
	origin, _ := store.CanonicalOrigin(repo.OriginURL(ctx))
	return store.Identity{ProjectID: projectID, Origin: origin, CommonDir: repo.CommonDir, Worktree: repo.Top}
}

// openProject loads the config and, when one exists, the store and this checkout's registration.
func openProject(ctx context.Context, env *Env, createStore bool) (*project, error) {
	repo, root, err := discover(ctx, env)
	if err != nil {
		return nil, err
	}
	p := &project{Repo: repo, RepoRoot: root}
	data, err := root.ReadFile(configPath, 1<<16)
	if errors.Is(err, fs.ErrNotExist) {
		p.Close()
		return nil, errNotInitialised
	}
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("reading %s: %w", configPath, err)
	}
	if p.Config, err = parseConfig(data); err != nil {
		p.Close()
		return nil, err
	}
	p.Identity = identityFor(ctx, repo, p.Config.ProjectID)
	dataDir, err := store.DataDir(env.Getenv)
	if err != nil {
		p.Close()
		return nil, err
	}
	p.Store, err = store.Resolve(ctx, dataDir, p.Identity, createStore)
	if errors.Is(err, store.ErrNoStore) {
		return p, nil
	}
	if err != nil {
		p.Close()
		return nil, err
	}
	decision, _, err := p.Store.TrustDecision(ctx, repo.CommonDir)
	if err != nil {
		p.Close()
		return nil, err
	}
	p.Trusted = decision == store.TrustTrusted
	co, ok, err := p.Store.LookupCheckout(ctx, p.Identity)
	if err != nil {
		p.Close()
		return nil, err
	}
	if !ok && p.Trusted {
		if known, _ := p.Store.CommonDirRegistered(ctx, repo.CommonDir); known {
			if co, err = p.Store.Register(ctx, p.Identity); err != nil {
				p.Close()
				return nil, err
			}
			ok = true
			p.Notices = append(p.Notices, "registered this worktree of an already trusted clone")
		}
	}
	p.Checkout, p.Registered = co, ok
	return p, nil
}

// publisher builds a publisher bound to this checkout. The fingerprint is the HEAD tree id.
func (p *project) publisher() *publish.Publisher {
	return &publish.Publisher{DB: p.Store.DB, Store: p.Store.Root, Repo: p.RepoRoot, CheckoutID: p.Checkout.ID,
		LockPath: filepath.Join(p.Store.Dir, "locks", "publish.lock"), LogPath: logPath,
		Fingerprint:  func(ctx context.Context) (string, error) { return p.Repo.HeadTree(ctx) },
		AbandonAfter: 10 * time.Minute, Now: time.Now}
}
