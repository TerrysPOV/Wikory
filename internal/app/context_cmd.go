package app

import (
	"context"
	"time"
)

func init() {
	register(Command{Name: "context", Summary: "print the orientation briefing for this checkout", Run: runContext})
}

func runContext(ctx context.Context, env *Env, args []string) (Result, error) {
	if len(args) != 0 {
		return Result{}, &UsageError{Msg: "context takes no arguments"}
	}
	p, err := openProject(ctx, env, false)
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	if !servable(p) {
		text := notRegisteredBriefing(p)
		return Result{Text: text, Data: map[string]any{"briefing": text, "registered": p.Registered, "trusted": p.Trusted}}, nil
	}
	text, err := buildBriefing(ctx, p, "full", time.Now().Add(30*time.Second))
	if err != nil {
		return Result{}, err
	}
	return Result{Text: text, Notices: p.Notices, Data: map[string]any{"briefing": text, "registered": true, "trusted": true}}, nil
}
