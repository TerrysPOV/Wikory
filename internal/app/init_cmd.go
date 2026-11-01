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
	repo, root, err := discover(ctx, env)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	if err := root.MkdirAll(pmDir, 0o755); err != nil {
		return Result{}, fmt.Errorf("creating %s: %w", pmDir, describeConfine(err))
	}
	var notices []string
	created := false
	var cfg ProjectConfig
	data, err := root.ReadFile(configPath, 1<<16)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		cfg = ProjectConfig{ProjectID: ids.ProjectID(), SchemaVersion: projectSchemaVersion}
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
	co, err := st.Register(ctx, id)
	if err != nil {
		return Result{}, err
	}
	decision, _, err := st.TrustDecision(ctx, repo.CommonDir)
	if err != nil {
		return Result{}, err
	}
	trusted := decision == store.TrustTrusted
	if created {
		if err := st.RecordTrust(ctx, repo.CommonDir, store.TrustTrusted, "created by wikory init"); err != nil {
			return Result{}, err
		}
		trusted = true
	} else if !trusted {
		if err := st.RecordTrust(ctx, repo.CommonDir, store.TrustUndecided, "project-memory existed before init on this machine"); err != nil {
			return Result{}, err
		}
		notices = append(notices, "project-memory/ already existed, so this checkout is registered but not trusted; "+
			"Wikory serves nothing from it until trust is granted (the wikory trust command arrives in a later version)")
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
	var written []string
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
	if written == nil {
		written = []string{}
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
