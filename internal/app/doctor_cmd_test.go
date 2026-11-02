package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type doctorData struct {
	Version      string       `json:"version"`
	Executable   string       `json:"executable"`
	Claude       string       `json:"claude_version"`
	KnownGood    bool         `json:"claude_known_good"`
	Mode         string       `json:"mode"`
	Capabilities Capabilities `json:"capabilities"`
	Initialised  bool         `json:"initialised"`
	Registered   bool         `json:"registered"`
	Trusted      bool         `json:"trusted"`
	Uncommitted  []string     `json:"uncommitted_wiki_changes"`
	HookErrors   int          `json:"hook_errors"`
	IndexPartial *bool        `json:"index_partial"`
	Transcripts  []struct {
		Path     string `json:"path"`
		Confined bool   `json:"confined"`
	} `json:"transcript_roots"`
}

func doctorJSON(t *testing.T, c *cli, dir string) doctorData {
	t.Helper()
	out, errOut, code := c.run(dir, "--json", "doctor")
	if code != ExitOK {
		t.Fatalf("doctor exit %d: %s", code, errOut)
	}
	var e struct {
		Data doctorData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &e); err != nil {
		t.Fatal(err)
	}
	return e.Data
}

func TestDoctorReportsModeAndUncommittedWiki(t *testing.T) {
	probeClaudeVersion = func(context.Context) string { return "2.1.316" }
	t.Cleanup(func() { probeClaudeVersion = defaultProbeClaudeVersion })
	dir, c := initRepoWithPage(t)
	d := doctorJSON(t, c, dir)
	if d.Mode != "full" || !d.KnownGood || !d.Capabilities.CanInjectOnSessionStart || !d.Initialised || !d.Registered || !d.Trusted || d.Executable == "" {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(strings.Join(d.Uncommitted, ","), "project-memory/wiki/retries.md") {
		t.Fatalf("uncommitted wiki change not reported: %v", d.Uncommitted)
	}
}

func TestDoctorListsTheApprovedTranscriptRoot(t *testing.T) {
	dir, c := initRepoWithPage(t)
	root := t.TempDir()
	c.extra["WIKORY_TRANSCRIPT_ROOTS"] = root
	d := doctorJSON(t, c, dir)
	if len(d.Transcripts) != 1 || d.Transcripts[0].Path != root || !d.Transcripts[0].Confined {
		t.Fatalf("%+v", d.Transcripts)
	}
}

func TestDoctorUnknownHarnessIsReduced(t *testing.T) {
	probeClaudeVersion = func(context.Context) string { return "9.9.9" }
	t.Cleanup(func() { probeClaudeVersion = defaultProbeClaudeVersion })
	dir, c := initRepoWithPage(t)
	if d := doctorJSON(t, c, dir); d.Mode != "reduced" || d.KnownGood {
		t.Fatalf("%+v", d)
	}
}

func TestDoctorOutsideARepoStillReportsTheBinary(t *testing.T) {
	d := doctorJSON(t, newCLI(t), t.TempDir())
	if d.Initialised || d.Version != "test" {
		t.Fatalf("%+v", d)
	}
}

func TestDoctorCountsHookErrorsAndStatesIndexCompleteness(t *testing.T) {
	dir, c := initRepoWithPage(t)
	log := filepath.Join(c.dataDir, "hook-errors.log")
	if err := os.WriteFile(log, []byte(logLine(time.Now().Add(-time.Minute), "stop")+logLine(time.Now().Add(-time.Second), "start")), 0o600); err != nil {
		t.Fatal(err)
	}
	d := doctorJSON(t, c, dir)
	if d.HookErrors != 2 || d.IndexPartial == nil || *d.IndexPartial {
		t.Fatalf("hook_errors=%d index_partial=%v", d.HookErrors, d.IndexPartial)
	}
}

func TestRecoverOnACleanCheckout(t *testing.T) {
	dir, c := initRepoWithPage(t)
	out, errOut, code := c.run(dir, "--json", "recover")
	if code != ExitOK || !strings.Contains(out, `"completed":[]`) || !strings.Contains(out, `"refused":[]`) {
		t.Fatalf("code=%d out=%s err=%s", code, out, errOut)
	}
}
