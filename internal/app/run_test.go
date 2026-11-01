package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func init() {
	register(Command{Name: "test-fail", Summary: "test only", Run: func(context.Context, *Env, []string) (Result, error) {
		return Result{Notices: []string{"a notice"}}, errors.New("deliberate failure")
	}})
	register(Command{Name: "test-echo", Summary: "test only", Run: func(_ context.Context, _ *Env, args []string) (Result, error) {
		return Result{Data: map[string]any{"args": args}, Text: strings.Join(args, ",")}, nil
	}})
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	env := &Env{IO: IO{In: strings.NewReader(""), Out: &out, Err: &errb}, Version: "1.2.3",
		Getenv: func(string) string { return "" }, Cwd: t.TempDir()}
	code = RunEnv(args, env)
	return out.String(), errb.String(), code
}

func decodeEnvelope(t *testing.T, s string) Envelope {
	t.Helper()
	var e Envelope
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		t.Fatalf("stdout is not a JSON envelope: %v\n%s", err, s)
	}
	return e
}

func TestVersionText(t *testing.T) {
	out, _, code := runCLI(t, "version")
	if code != ExitOK || out != "wikory 1.2.3\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestJSONFlagBeforeOrAfterCommand(t *testing.T) {
	for _, args := range [][]string{{"--json", "version"}, {"version", "--json"}} {
		out, _, code := runCLI(t, args...)
		e := decodeEnvelope(t, out)
		data, _ := e.Data.(map[string]any)
		if code != ExitOK || e.Schema != "wikory.version/v1" || !e.OK || data["version"] != "1.2.3" {
			t.Fatalf("args=%v code=%d envelope=%+v", args, code, e)
		}
	}
}

func TestErrorEnvelopeAndExitCode(t *testing.T) {
	out, _, code := runCLI(t, "--json", "test-fail")
	e := decodeEnvelope(t, out)
	if code != ExitFailure || e.OK || e.Error == nil || e.Error.Code != "failure" ||
		e.Error.Message != "deliberate failure" || len(e.Notices) != 1 {
		t.Fatalf("code=%d envelope=%+v", code, e)
	}
}

func TestUnknownCommandIsUsageError(t *testing.T) {
	_, errOut, code := runCLI(t, "nope")
	if code != ExitUsage || !strings.Contains(errOut, `unknown command "nope"`) {
		t.Fatalf("code=%d stderr=%q", code, errOut)
	}
}

func TestJSONAfterDoubleDashIsAnArgument(t *testing.T) {
	out, _, code := runCLI(t, "test-echo", "a", "--", "--json")
	if code != ExitOK || out != "a,--json\n" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
