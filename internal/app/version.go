package app

import (
	"context"
	"runtime"
)

func init() {
	register(Command{Name: "version", Summary: "print the version", Run: func(_ context.Context, env *Env, _ []string) (Result, error) {
		return Result{
			Data: map[string]string{"version": env.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH},
			Text: "wikory " + env.Version,
		}, nil
	}})
}
