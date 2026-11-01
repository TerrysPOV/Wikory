package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TerrysPOV/Wikory/internal/claim"
	"github.com/TerrysPOV/Wikory/internal/confine"
	"github.com/TerrysPOV/Wikory/internal/ids"
	"github.com/TerrysPOV/Wikory/internal/publish"
	"github.com/TerrysPOV/Wikory/internal/validate"
)

func init() {
	register(Command{Name: "page", Summary: "page tools: page stamp <path> renders claim metadata", Run: runPage})
}

type stampReport struct {
	Path     string   `json:"path"`
	Stamped  int      `json:"claims_stamped"`
	Findings []string `json:"findings"`
}

func runPage(ctx context.Context, env *Env, args []string) (Result, error) {
	if len(args) < 2 || args[0] != "stamp" {
		return Result{}, &UsageError{Msg: "usage: wikory page stamp <project-memory/wiki/page.md>..."}
	}
	for _, rel := range args[1:] {
		if _, err := confine.Split(rel); err != nil || !strings.HasPrefix(rel, wikiDir+"/") || !strings.HasSuffix(rel, ".md") {
			return Result{}, &UsageError{Msg: fmt.Sprintf("%q is not a page under %s/", rel, wikiDir)}
		}
	}
	p, err := openProject(ctx, env, false)
	if err != nil {
		return Result{}, err
	}
	defer p.Close()
	if !servable(p) {
		return Result{}, errors.New("this checkout is not registered and trusted; run wikory init")
	}
	pub := p.publisher()
	if _, err := pub.Recover(ctx); err != nil {
		return Result{}, err
	}
	var reports []stampReport
	var notices []string
	var text strings.Builder
	for _, rel := range args[1:] {
		rep, err := stampPage(ctx, p, pub, rel)
		if err != nil {
			return Result{}, fmt.Errorf("%s: %w", rel, describeConfine(err))
		}
		reports = append(reports, rep)
		for _, f := range rep.Findings {
			notices = append(notices, rel+": "+f)
		}
		fmt.Fprintf(&text, "%s: %d claim(s) stamped\n", rel, rep.Stamped)
	}
	return Result{Text: text.String(), Notices: notices, Data: map[string]any{"pages": reports}}, nil
}

func stampPage(ctx context.Context, p *project, pub *publish.Publisher, rel string) (stampReport, error) {
	rep := stampReport{Path: rel, Findings: []string{}}
	data, err := p.RepoRoot.ReadFile(rel, 1<<20)
	if err != nil {
		return rep, err
	}
	page := claim.ParsePage(rel, data)
	lines := strings.Split(string(data), "\n")
	env := p.validateEnv()
	changed := false
	for _, b := range page.Blocks {
		if b.Err != nil {
			rep.Findings = append(rep.Findings, fmt.Sprintf("line %d left unchanged: %v", b.Line, b.Err))
			continue
		}
		authored, findings, err := validate.Authoring(ctx, b.Claim, b.Text, env, ids.NewULID)
		if err != nil {
			return rep, err
		}
		for _, f := range findings {
			rep.Findings = append(rep.Findings, fmt.Sprintf("line %d (rule %d): %s", b.Line, f.Rule, f.Msg))
		}
		rendered := authored.Render()
		idx := b.Line - 1
		orig := lines[idx]
		cr := strings.HasSuffix(orig, "\r")
		if strings.TrimSpace(strings.TrimSuffix(orig, "\r")) == rendered {
			continue
		}
		indent := orig[:len(orig)-len(strings.TrimLeft(orig, " \t"))]
		lines[idx] = indent + rendered
		if cr {
			lines[idx] += "\r"
		}
		rep.Stamped++
		changed = true
	}
	if !changed {
		return rep, nil
	}
	job, err := pub.Begin(ctx, "stamp")
	if err != nil {
		return rep, err
	}
	op := publish.Op{Target: rel, OldHash: publish.HashContent(data), Content: []byte(strings.Join(lines, "\n"))}
	return rep, pub.Publish(ctx, job, []publish.Op{op}, "stamp "+rel)
}
