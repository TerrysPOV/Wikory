package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/bootstrap"
)

func init() {
	register(Command{Name: "bootstrap", Summary: "bootstrap: build a baseline wiki (plan, next, submit, confirm, finish, status)", Run: runBootstrap})
}

const bootstrapUsage = "usage: wikory bootstrap plan|next|submit|confirm|finish|status [flags]"

func runBootstrap(ctx context.Context, env *Env, args []string) (Result, error) {
	if len(args) == 0 {
		return Result{}, &UsageError{Msg: bootstrapUsage}
	}
	switch args[0] {
	case "plan":
		return bootstrapPlan(ctx, env, args[1:])
	case "next":
		return bootstrapNext(ctx, env, args[1:])
	case "submit":
		return bootstrapSubmit(ctx, env, args[1:])
	case "confirm":
		return bootstrapConfirm(ctx, env, args[1:])
	case "finish":
		return bootstrapFinish(ctx, env, args[1:])
	case "status":
		return bootstrapStatus(ctx, env, args[1:])
	}
	return Result{}, &UsageError{Msg: fmt.Sprintf("unknown bootstrap subcommand %q; %s", args[0], bootstrapUsage)}
}

// parseFlags reads --name value and --name=value pairs for the names in want. A repeated name
// accumulates. Anything else, including a stray positional, is a usage error.
func parseFlags(args []string, want map[string]bool) (map[string][]string, error) {
	out := map[string][]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			return nil, &UsageError{Msg: fmt.Sprintf("unexpected argument %q", a)}
		}
		name, val, hasVal := strings.Cut(a[2:], "=")
		if !want[name] {
			return nil, &UsageError{Msg: fmt.Sprintf("unknown flag --%s", name)}
		}
		if !hasVal {
			if i+1 >= len(args) {
				return nil, &UsageError{Msg: fmt.Sprintf("--%s needs a value", name)}
			}
			i++
			val = args[i]
		}
		out[name] = append(out[name], val)
	}
	return out, nil
}

// newRuns binds the run store to this checkout and the store's write lock.
func newRuns(p *project) *bootstrap.Runs {
	return bootstrap.NewRuns(p.Store.DB, p.Checkout.ID, p.Store.WithWriteLock)
}
