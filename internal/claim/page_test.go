package claim

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const samplePage = "---\r\n" +
	"id: queue\r\n" +
	"type: component\r\n" +
	"title: Job queue\r\n" +
	"links: [decision-retries, failure-double-ack]\r\n" +
	"colour: blue\r\n" +
	"---\r\n" +
	"# Job queue\r\n" +
	"Intro prose about the queue.\r\n" +
	"<!-- an editor note -->\r\n" +
	"## Acknowledgement\r\n" +
	"<!-- claim kind=current-impl status=unverified ev=src/queue.go dep=src/queue.go#Ack -->\r\n" +
	"Jobs are acknowledged only after the handler returns,\r\n" +
	"so a crash replays the job.\r\n" +
	"\r\n" +
	"<!-- claim kind=intent status=verified dep=a--b -->\r\n" +
	"Broken metadata.\r\n" +
	"\r\n" +
	"<!-- claim kind=rationale status=unverified -->\r\n" +
	"\r\n" +
	"Closing prose.\r\n"

func TestParsePage(t *testing.T) {
	p := ParsePage("project-memory/wiki/queue.md", []byte(samplePage))
	if p.ID != "queue" || p.Type != "component" || p.Title != "Job queue" ||
		!reflect.DeepEqual(p.Links, []string{"decision-retries", "failure-double-ack"}) {
		t.Fatalf("frontmatter: %+v", p)
	}
	if len(p.Blocks) != 2 {
		t.Fatalf("want 2 blocks (one good, one malformed), got %d: %+v", len(p.Blocks), p.Blocks)
	}
	good := p.Blocks[0]
	if good.Err != nil || good.Heading != "Acknowledgement" || good.Claim.Kind != "current-impl" ||
		good.Text != "Jobs are acknowledged only after the handler returns,\nso a crash replays the job." {
		t.Fatalf("good block: %+v", good)
	}
	if !errors.Is(p.Blocks[1].Err, ErrMalformed) || p.Blocks[1].Text != "Broken metadata." {
		t.Fatalf("malformed block: %+v", p.Blocks[1])
	}
	msgs := ""
	for _, f := range p.Findings {
		msgs += f.Msg + "\n"
	}
	for _, want := range []string{"unknown frontmatter key", "claim comment has no claim text", "malformed"} {
		if !strings.Contains(msgs, want) {
			t.Errorf("missing finding %q in:\n%s", want, msgs)
		}
	}
	if strings.Contains(p.Body, "acknowledged") || strings.Contains(p.Body, "editor note") ||
		!strings.Contains(p.Body, "Intro prose") || !strings.Contains(p.Body, "Closing prose") {
		t.Fatalf("body: %q", p.Body)
	}
}

func TestPageWithoutFrontmatter(t *testing.T) {
	p := ParsePage("project-memory/wiki/notes.md", []byte("# Notes\n\nSome text.\n"))
	if p.Title != "Notes" || p.Type != "concept" || p.ID != "notes" {
		t.Fatalf("%+v", p)
	}
}

func TestBadPageType(t *testing.T) {
	p := ParsePage("x.md", []byte("---\ntype: essay\n---\n"))
	if p.Type != "concept" || len(p.Findings) == 0 {
		t.Fatalf("%+v", p)
	}
}
