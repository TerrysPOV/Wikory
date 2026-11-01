// Package app is Wikory's single application layer. The CLI, hooks and (later) MCP are
// thin wrappers over the commands registered here (spec 3, operations layer).
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// IO bundles the process streams so commands are testable without a terminal.
type IO struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// Exit codes. Hook commands always exit ExitOK (hook_cmd.go).
const (
	ExitOK      = 0
	ExitFailure = 1
	ExitUsage   = 2
)

// Env is what every command receives.
type Env struct {
	IO      IO
	JSON    bool
	Version string
	Getenv  func(string) string
	Cwd     string
}

// Result is a command's output: Data for --json, Text for people, Notices for stderr.
type Result struct {
	Data    any
	Text    string
	Notices []string
}

// Command is one CLI verb. Raw commands (hooks) write their own stdout and bypass the envelope.
type Command struct {
	Name    string
	Summary string
	Run     func(ctx context.Context, env *Env, args []string) (Result, error)
	Raw     bool
}

// UsageError marks a mistake in how the command was called.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

var registry = map[string]Command{}

// register is called from each command file's init function.
func register(c Command) {
	if _, dup := registry[c.Name]; dup {
		panic("app: duplicate command " + c.Name)
	}
	registry[c.Name] = c
}

// Run is the process entry point.
func Run(args []string, stdio IO, version string) int {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stdio.Err, "wikory: cannot read the working directory: %v\n", err)
		return ExitFailure
	}
	return RunEnv(args, &Env{IO: stdio, Version: version, Getenv: os.Getenv, Cwd: cwd})
}

// RunEnv runs one command with an explicit environment.
func RunEnv(args []string, env *Env) int {
	args, env.JSON = extractJSONFlag(args)
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(env.IO.Err)
		if len(args) == 0 {
			return ExitUsage
		}
		return ExitOK
	}
	cmd, ok := registry[args[0]]
	if !ok {
		fmt.Fprintf(env.IO.Err, "wikory: unknown command %q\n", args[0])
		printUsage(env.IO.Err)
		return ExitUsage
	}
	res, err := cmd.Run(context.Background(), env, args[1:])
	if cmd.Raw && err == nil && !env.JSON {
		return ExitOK
	}
	return finish(env, cmd.Name, res, err)
}

// extractJSONFlag removes every --json that appears before a "--" terminator.
func extractJSONFlag(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	found, terminated := false, false
	for _, a := range args {
		if !terminated && a == "--" {
			terminated = true
			continue
		}
		if !terminated && a == "--json" {
			found = true
			continue
		}
		out = append(out, a)
	}
	return out, found
}

func finish(env *Env, name string, res Result, err error) int {
	if err != nil {
		code, exit := "failure", ExitFailure
		var ue *UsageError
		if errors.As(err, &ue) {
			code, exit = "usage", ExitUsage
		}
		if env.JSON {
			writeEnvelope(env.IO.Out, Envelope{Schema: schemaFor(name), OK: false,
				Error: &ErrBody{Code: code, Message: err.Error()}, Notices: res.Notices})
		} else {
			writeNotices(env.IO.Err, res.Notices)
			fmt.Fprintf(env.IO.Err, "wikory %s: %v\n", name, err)
		}
		return exit
	}
	if env.JSON {
		writeEnvelope(env.IO.Out, Envelope{Schema: schemaFor(name), OK: true, Data: res.Data, Notices: res.Notices})
		return ExitOK
	}
	writeNotices(env.IO.Err, res.Notices)
	if res.Text != "" {
		fmt.Fprint(env.IO.Out, res.Text)
		if res.Text[len(res.Text)-1] != '\n' {
			fmt.Fprintln(env.IO.Out)
		}
	}
	return ExitOK
}

func printUsage(w io.Writer) {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "usage: wikory [--json] <command> [args]")
	for _, n := range names {
		fmt.Fprintf(w, "  %-14s %s\n", n, registry[n].Summary)
	}
}
