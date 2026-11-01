package app

import (
	"context"
	"errors"
	"fmt"
)

func init() {
	register(Command{Name: "recover", Summary: "finish or report interrupted publishes in this checkout", Run: runRecover})
}

func runRecover(ctx context.Context, env *Env, args []string) (Result, error) {
	p, err := openProject(ctx, env, false)
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	if p.Store == nil || !p.Registered {
		return Result{}, errors.New("this checkout is not registered; nothing to recover")
	}
	rep, err := p.publisher().Recover(ctx)
	if err != nil {
		return Result{}, err
	}
	nz := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	// Recovery re-checks the tree fingerprint and takes the lease over under a new epoch before it
	// re-runs a job (hardened publish recovery). A refused or conflicted job is never reported as
	// completed: each gets its own notice so it cannot pass unseen.
	var notices []string
	for _, id := range rep.Conflicts {
		notices = append(notices, "job "+id+" stopped on a conflict with a human edit; inspect the files it names before editing them")
	}
	for _, id := range rep.Refused {
		notices = append(notices, "job "+id+" was refused: the tree changed (branch switch or new commit) since it began; it was not applied")
	}
	return Result{
		Text:    fmt.Sprintf("recovered: %d completed, %d conflicts, %d refused, %d abandoned\n", len(rep.Completed), len(rep.Conflicts), len(rep.Refused), len(rep.Abandoned)),
		Notices: notices,
		Data:    map[string]any{"completed": nz(rep.Completed), "conflicts": nz(rep.Conflicts), "refused": nz(rep.Refused), "abandoned": nz(rep.Abandoned)},
	}, nil
}
