package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/publish"
	"github.com/TerrysPOV/Wikory/internal/store"
)

func init() {
	register(Command{Name: "init", Summary: "set up project-memory/ and the private store for this repository", Run: runInit})
}

// newProjectID mints a project id. A test replaces it to pre-seed the store of a project that
// does not exist yet.
var newProjectID = ids.ProjectID

const logHeader = "# Wikory publish log\n\nOne line per publish, appended by wikory. Merged with merge=union.\n\n"

const schemaDoc = `# Claim format (for people writing pages by hand)

Each claim is one paragraph preceded by one metadata comment line:

    <!-- claim kind=current-impl status=verified ev=src/queue.go dep=src/queue.go#Enqueue -->
    Jobs are acknowledged only after the handler returns, so a crash replays the job.

kind is current-impl, intent, rationale or hypothesis. status is verified, unverified,
conflicting, historical or superseded. ev lists evidence (a committed path, commit:<sha>),
dep lists the paths the claim depends on. Separate list items with ";".

After writing or editing claims, run wikory page stamp <path>. Wikory then mints the id,
resolves evidence to committed blob ids, computes the text hash and writes the metadata.
A model never writes this line, and neither should you beyond kind, status, ev and dep.
`

const wikiReadme = "# Project wiki\n\nOne topic per file. See ../schema/claims.md for the claim format.\n"

func runInit(ctx context.Context, env *Env, args []string) (Result, error) {
	if len(args) != 0 {
		return Result{}, &UsageError{Msg: "init takes no arguments"}
	}
	var notices []string
	repo, root, err := discover(ctx, env)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	if !repo.Confirmed {
		return Result{}, errUnconfirmed
	}
	// Judge emptiness before anything is created: a missing or empty project-memory/ is
	// ours to trust, any entry (a dotfile or a symlink included) came from elsewhere.
	pmEmpty := true
	switch ents, err := root.ReadDir(pmDir); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Result{}, fmt.Errorf("reading %s: %w", pmDir, describeConfine(err))
	default:
		pmEmpty = len(ents) == 0
	}
	if err := root.MkdirAll(pmDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("creating %s: %w", pmDir, describeConfine(err))
	}
	created := false
	var cfg ProjectConfig
	data, err := root.ReadFile(configPath, 1<<16)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		cfg = ProjectConfig{ProjectID: newProjectID(), SchemaVersion: projectSchemaVersion}
		cfg.Retrieval.BudgetTokens = defaultBudgetTokens
		created = true
	case err != nil:
		return Result{}, fmt.Errorf("reading %s: %w", configPath, describeConfine(err))
	default:
		if cfg, err = parseConfig(data); err != nil {
			return Result{}, err
		}
	}
	id := identityFor(ctx, repo, cfg.ProjectID)
	dataDir, err := store.DataDir(env.Getenv)
	if err != nil {
		return Result{}, err
	}
	st, err := store.Resolve(ctx, dataDir, id, true)
	if err != nil {
		return Result{}, err
	}
	defer st.Close()
	replaced, err := ensureCloneID(ctx, st, repo.CommonDir)
	if err != nil {
		return Result{}, err
	}
	if replaced {
		// Another repository now sits where a registered clone was: nothing the store holds
		// for that path, its trust decision included, is this repository's.
		if err := st.ForgetClone(ctx, repo.CommonDir); err != nil {
			return Result{}, err
		}
		notices = append(notices, "a different repository now occupies the path of a clone registered earlier; its trust decision was discarded")
	}
	co, err := st.Register(ctx, id)
	if err != nil {
		return Result{}, err
	}
	notices = append(notices, st.Notices...)
	decision, recorded, err := st.TrustDecision(ctx, repo.CommonDir)
	if err != nil {
		return Result{}, err
	}
	trusted := decision == store.TrustTrusted
	switch {
	case recorded:
		// init never changes a decision that already exists (spec 10.4).
		notices = append(notices, fmt.Sprintf("trust for this clone is already recorded as %q and init leaves it unchanged", decision))
		if !trusted {
			notices = append(notices, "this clone is not trusted, so the committed wiki is withheld until trust is granted")
		}
	case created && pmEmpty:
		if err := st.RecordTrust(ctx, repo.CommonDir, store.TrustTrusted, "created by wikory init"); err != nil {
			return Result{}, err
		}
		trusted = true
	default:
		if err := st.RecordTrust(ctx, repo.CommonDir, store.TrustUndecided, "project-memory existed before init"); err != nil {
			return Result{}, err
		}
		notices = append(notices, "project-memory/ already existed, so this checkout is registered but not trusted yet; "+
			"the committed wiki is withheld until trust is granted (the wikory trust command arrives in a later version)")
	}
	want := []struct {
		path    string
		content []byte
	}{
		{configPath, renderConfig(cfg)},
		{gitattributesPath, []byte(gitattributesContent)},
		{logPath, []byte(logHeader)},
		{pmDir + "/schema/claims.md", []byte(schemaDoc)},
		{wikiDir + "/README.md", []byte(wikiReadme)},
	}
	var ops []publish.Op
	written := []string{}
	for _, w := range want {
		existing, err := root.ReadFile(w.path, 1<<20)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			ops = append(ops, publish.Op{Target: w.path, OldHash: publish.Absent, Content: w.content})
			written = append(written, w.path)
		case err != nil:
			return Result{}, fmt.Errorf("%s: %w", w.path, describeConfine(err))
		case w.path == gitattributesPath && string(existing) != gitattributesContent:
			notices = append(notices, "project-memory/.gitattributes differs from Wikory's; it needs: "+strings.TrimSpace(strings.ReplaceAll(gitattributesContent, "\n", "; ")))
		}
	}
	p := &project{Repo: repo, RepoRoot: root, Config: cfg, Identity: id, Store: st, Checkout: co, Registered: true, Trusted: trusted}
	pub := p.publisher()
	if _, err := pub.Recover(ctx); err != nil {
		return Result{}, err
	}
	if len(ops) > 0 {
		job, err := pub.Begin(ctx, "init")
		if err != nil {
			return Result{}, err
		}
		if err := pub.Publish(ctx, job, ops, "init project="+cfg.ProjectID); err != nil {
			return Result{}, err
		}
	}
	text := fmt.Sprintf("Wikory initialised in %s\n  project id: %s\n  private store: %s\n  trusted: %v\n",
		repo.Top, cfg.ProjectID, st.Dir, trusted)
	if len(written) > 0 {
		text += "  wrote: " + strings.Join(written, ", ") + "\nWikory never commits: review and commit project-memory/ yourself.\n"
	}
	return Result{Text: text, Notices: notices, Data: map[string]any{
		"project_id": cfg.ProjectID, "created": created, "trusted": trusted, "store_dir": st.Dir,
		"checkout_id": co.ID, "files_written": written,
	}}, nil
}

// describeConfine turns confinement errors into plain messages.
func describeConfine(err error) error {
	switch {
	case errors.Is(err, confine.ErrSymlink):
		return fmt.Errorf("refused a symlink (Wikory never follows symlinks): %w", err)
	case errors.Is(err, confine.ErrNotRegular):
		return fmt.Errorf("refused a non-regular file: %w", err)
	}
	return err
}
