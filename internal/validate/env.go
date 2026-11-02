package validate

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/gitx"
)

// GitEnv answers the rules' questions from git and the confined working tree.
type GitEnv struct {
	Repo     *gitx.Repo
	Root     *confine.Root
	MaxBytes int64
}

func (g GitEnv) HeadBlobs(ctx context.Context, paths []string) (map[string]string, error) {
	return g.Repo.HeadBlobs(ctx, paths)
}

// WorkBlob hashes the working-tree file as git would. A missing, symlinked, escaping,
// non-regular or oversized file reads as absent: it is never followed (rule 7).
func (g GitEnv) WorkBlob(ctx context.Context, path string) (string, bool, error) {
	data, err := g.Root.ReadFile(path, g.MaxBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, confine.ErrSymlink) || errors.Is(err, confine.ErrNotRegular) ||
			errors.Is(err, confine.ErrEscape) || errors.Is(err, confine.ErrNotDir) || errors.Is(err, confine.ErrTooLarge) {
			return "", false, nil
		}
		return "", false, err
	}
	oid, err := g.Repo.HashBlob(ctx, path, data)
	if err != nil {
		return "", false, err
	}
	return oid, true, nil
}

func (g GitEnv) CommitExists(ctx context.Context, sha string) (bool, error) {
	ok, _ := g.Repo.CommitExists(ctx, sha)
	return ok, nil
}

func (g GitEnv) HeadCommit(ctx context.Context) (string, error) { return g.Repo.HeadCommit(ctx) }

func (g GitEnv) ObjectFormat(ctx context.Context) (string, error) { return g.Repo.ObjectFormat(ctx) }

// ResolveObject maps gitx's ambiguity error onto ErrAmbiguousID.
func (g GitEnv) ResolveObject(ctx context.Context, id, kind string) (string, error) {
	full, err := g.Repo.ResolveObject(ctx, id, kind)
	if errors.Is(err, gitx.ErrAmbiguous) {
		return "", fmt.Errorf("%w: %v", ErrAmbiguousID, err)
	}
	return full, err
}
