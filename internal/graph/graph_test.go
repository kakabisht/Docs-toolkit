package graph

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docsbot/docsbot/internal/crawl"
)

func build(t *testing.T) *Graph {
	t.Helper()
	s, err := crawl.LoadLocal(filepath.Join("..", "..", "testdata", "site"), crawl.LocalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return Build(s)
}

func TestBuildAndStore(t *testing.T) {
	g := build(t)
	if g.Entry != "/" {
		t.Errorf("entry %q", g.Entry)
	}
	top := g.TopByRank(1)[0]
	if top.Key != "/guide/install" || top.Rank != 1 {
		t.Errorf("top page %s %.2f", top.Key, top.Rank)
	}
	if n := g.Node("/misc/orphan"); n.In != 0 || n.Depth != -1 {
		t.Errorf("orphan in=%d depth=%d", n.In, n.Depth)
	}
	if n := g.Node("/guide/configure"); n.Depth != 2 {
		t.Errorf("configure depth = %d", n.Depth)
	}
	db := filepath.Join(t.TempDir(), "g.db")
	if err := g.Save(db); err != nil {
		t.Fatal(err)
	}
	h, err := Load(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Nodes) != len(g.Nodes) || len(h.Edges) != len(g.Edges) || h.Entry != g.Entry || !h.LLMSTxt.Found {
		t.Fatalf("roundtrip: %d/%d nodes, %d/%d edges", len(h.Nodes), len(g.Nodes), len(h.Edges), len(g.Edges))
	}
	if !h.Node("/api/endpoints").Anchors["custom-anchor"] {
		t.Error("anchors not stored")
	}
	if a, b := g.ComputeStats(5), h.ComputeStats(5); a.PageEdges != b.PageEdges || len(a.Orphans) != len(b.Orphans) {
		t.Errorf("stats differ after load: %+v vs %+v", a, b)
	}
}

func TestExports(t *testing.T) {
	g := build(t)
	for name, f := range map[string]func(*bytes.Buffer) error{
		"mermaid": func(b *bytes.Buffer) error { return g.Mermaid(b, ExportOptions{Sections: true}) },
		"dot":     func(b *bytes.Buffer) error { return g.DOT(b, ExportOptions{}) },
		"json":    func(b *bytes.Buffer) error { return g.JSON(b, ExportOptions{Sections: true}) },
		"html":    func(b *bytes.Buffer) error { return g.HTML(b, ExportOptions{}) },
		"llms":    func(b *bytes.Buffer) error { return g.WriteLLMSTxt(b, "") },
	} {
		var b bytes.Buffer
		if err := f(&b); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out := b.String()
		if !strings.Contains(out, "Install") {
			t.Errorf("%s export lacks page titles:\n%s", name, out)
		}
	}
}

func TestPageRankSumsAndOrder(t *testing.T) {
	nodes := []*Node{{Key: "a"}, {Key: "b"}, {Key: "c"}}
	adj := map[string]map[string]bool{"a": {"c": true}, "b": {"c": true}, "c": {"a": true}}
	pr := PageRank(nodes, adj, 0.85, 50)
	if !(pr["c"] == 1 && pr["a"] > pr["b"]) {
		t.Errorf("pagerank %v", pr)
	}
}
