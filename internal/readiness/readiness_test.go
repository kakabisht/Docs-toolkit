package readiness

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/graph"
	"github.com/kakabisht/Docs-toolkit/internal/links"
)

func TestGraphChecks(t *testing.T) {
	s, err := crawl.LoadLocal(filepath.Join("..", "..", "testdata", "site"), crawl.LocalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	g := graph.Build(s)
	lr := links.Check(context.Background(), g, nil, links.Options{})
	checks := GraphChecks(g, lr, Options{})
	by := map[string]Check{}
	for _, c := range checks {
		by[c.ID] = c
	}
	expect := map[string]string{
		"llms-txt":          Pass,
		"llms-txt-coverage": Fail, // 2 of 7 pages listed
		"orphan-pages":      Fail, // 1 of 7
		"internal-links":    Fail,
		"page-titles":       Pass,
		"heading-structure": Warn, // endpoints jumps from the title to H4
		"code-languages":    Fail, // 1 of 2 untagged
		"link-text":         Fail, // "here"
	}
	for id, want := range expect {
		if got := by[id].Status; got != want {
			t.Errorf("%s = %s (%s), want %s", id, got, by[id].Message, want)
		}
	}
	r := Combine(g.Source, checks, nil, "", 0.6)
	if r.Score != r.GraphScore || r.Score <= 0 || r.Score >= 100 || len(r.Fixes) == 0 {
		t.Errorf("combine: %+v", r)
	}
	af := &AfdocsResult{Scoring: &AfdocsScoring{Overall: 50, Grade: "F"}}
	r = Combine(g.Source, checks, af, "", 0.5)
	if want := (50 + r.GraphScore + 1) / 2; r.Score != want && r.Score != want-1 {
		t.Errorf("weighted score %d, graph %d", r.Score, r.GraphScore)
	}
}
