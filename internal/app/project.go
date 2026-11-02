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
	// errUnconfirmed: git does not vouch that this directory is a worktree of the clone its
	// .git points at, so Wikory neither serves, captures nor registers it.
	errUnconfirmed = &UsageError{Msg: "git does not list this directory as a worktree of the repository its .git points at, so Wikory will not use it"}
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

// cloneIDFile holds, inside a clone's git directory, the nonce the private store recorded when
// the clone was registered. Trust is keyed on the clone's path, and a path can be reused by a
// different repository; the nonce is what a replacement cannot carry over.
const cloneIDFile = "wikory-checkout-id"

// testHookCloneID lets a test fail ensureCloneID at a named step. Nil in production.
var testHookCloneID func(stage string) error

func cloneIDStage(stage string) error {
	if f := testHookCloneID; f != nil {
		return f(stage)
	}
	return nil
}

// readCloneID returns the nonce in commonDir, or "" when the file is absent or unreadable.
func readCloneID(commonDir string) string {
	root, err := confine.Open(commonDir)
	if err != nil {
		return ""
	}
	defer root.Close()
	data, err := root.ReadFile(cloneIDFile, 256)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// ensureCloneID makes the store and the git directory agree on a nonce for this clone and
// returns it. When the nonce in the git directory matches the store's record the clone is
// verified and nothing changes. In every other case the clone cannot be verified: either the
// store knew a different nonce (another repository now occupies the path) or it knew none
// while holding trust or registrations for the path (a record from before clone ids). Then
// revoked is true. The order is fixed so that no failure leaves the path trusted: the store
// revokes everything and binds the new nonce in one transaction, and only then is the file
// written. A failure before the transaction commits changes nothing, and the old record no
// longer matches the file; a failure after it leaves a nonce the file does not carry.
func ensureCloneID(ctx context.Context, st *store.Store, commonDir string) (cloneID string, revoked bool, err error) {
	want, known, err := st.CloneID(ctx, commonDir)
	if err != nil {
		return "", false, err
	}
	if known && want != "" && readCloneID(commonDir) == want {
		return want, false, nil
	}
	revoked = known
	if !known {
		if revoked, err = st.HasCloneRecords(ctx, commonDir); err != nil {
			return "", false, err
		}
	}
	id := "wkc_" + ids.NewULID()
	if err := cloneIDStage("before-revoke"); err != nil {
		return "", false, err
	}
	if err := st.RebindClone(ctx, commonDir, id); err != nil {
		return "", false, err
	}
	if err := cloneIDStage("after-revoke"); err != nil {
		return "", false, err
	}
	root, err := confine.Open(commonDir)
	if err != nil {
		return "", false, err
	}
	defer root.Close()
	if err := root.WriteFileAtomic(cloneIDFile, []byte(id+"\n"), 0o600); err != nil {
		return "", false, fmt.Errorf("recording the clone id in %s: %w", commonDir, err)
	}
	if err := cloneIDStage("after-file"); err != nil {
		return "", false, err
	}
	return id, revoked, nil
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
	if errors.Is(err, gitx.ErrWorktreeRedirected) {
		return nil, nil, &UsageError{Msg: "this repository's config (core.worktree) puts its work tree outside the current directory; Wikory will not write there"}
	}
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
	if err := p.load(ctx, env, createStore); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// load fills in p from the committed config and the private store. A missing store is not an
// error: p.Store stays nil and p is neither registered nor trusted.
func (p *project) load(ctx context.Context, env *Env, createStore bool) error {
	data, err := p.RepoRoot.ReadFile(configPath, 1<<16)
	if errors.Is(err, fs.ErrNotExist) {
		return errNotInitialised
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", configPath, err)
	}
	if p.Config, err = parseConfig(data); err != nil {
		return err
	}
	p.Identity = identityFor(ctx, p.Repo, p.Config.ProjectID)
	dataDir, err := store.DataDir(env.Getenv)
	if err != nil {
		return err
	}
	p.Store, err = store.Resolve(ctx, dataDir, p.Identity, createStore)
	if errors.Is(err, store.ErrNoStore) {
		return nil
	}
	if err != nil {
		return err
	}
	p.Notices = append(p.Notices, p.Store.Notices...)
	decision, _, err := p.Store.TrustDecision(ctx, p.Repo.CommonDir)
	if err != nil {
		return err
	}
	p.Trusted = decision == store.TrustTrusted
	// The store must have registered this very clone, not just a clone at this path.
	want, known, err := p.Store.CloneID(ctx, p.Repo.CommonDir)
	if err != nil {
		return err
	}
	if !known || want == "" || readCloneID(p.Repo.CommonDir) != want {
		p.Trusted = false
		p.Notices = append(p.Notices, "this path was not registered as this repository (it may have been replaced); run wikory init to register it")
		return nil
	}
	p.Identity.CloneID = want
	if !p.Repo.Confirmed {
		// Never registered, never trusted, whatever the store says about the common dir the
		// .git file names. Nothing is served and nothing captured.
		p.Trusted = false
		p.Notices = append(p.Notices, errUnconfirmed.Msg)
		return nil
	}
	co, ok, err := p.Store.LookupCheckout(ctx, p.Identity)
	if err != nil {
		return err
	}
	if !ok && p.Trusted {
		if known, _ := p.Store.CommonDirRegistered(ctx, p.Repo.CommonDir); known {
			if co, err = p.Store.Register(ctx, p.Identity); err != nil {
				return err
			}
			ok = true
			p.Notices = append(p.Notices, "registered this worktree of an already trusted clone")
		}
	}
	p.Checkout, p.Registered = co, ok
	return nil
}

// publisher builds a publisher bound to this checkout. The fingerprint is the HEAD tree id.
func (p *project) publisher() *publish.Publisher {
	return &publish.Publisher{DB: p.Store.DB, Store: p.Store.Root, Repo: p.RepoRoot, CheckoutID: p.Checkout.ID,
		LockPath: filepath.Join(p.Store.Dir, "locks", "publish.lock"), LogPath: logPath,
		Fingerprint:  func(ctx context.Context) (string, error) { return p.Repo.HeadTree(ctx) },
		AbandonAfter: 10 * time.Minute, Now: time.Now}
}
