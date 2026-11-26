package kg

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/vocab"
)

var update = flag.Bool("update", false, "rewrite golden files")

const fixture = "../../testdata/fixtures/graph"

func buildFixture(t *testing.T) *Graph {
	t.Helper()
	s, err := crawl.LoadLocal(fixture, crawl.LocalOptions{NoGit: true})
	if err != nil {
		t.Fatal(err)
	}
	v, err := vocab.Load(filepath.Join(fixture, "vocab.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	g := Build(s, Options{Vocab: v, Repo: "graph-fixture"})
	g.Source = "testdata/fixtures/graph"
	return g
}

func TestGolden(t *testing.T) {
	g := buildFixture(t)
	var buf bytes.Buffer
	if err := g.JSON(&buf); err != nil {
		t.Fatal(err)
	}
	golden := filepath.Join(fixture, "expected.json")
	if *update {
		if err := os.WriteFile(golden, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Errorf("graph differs from %s; run go test ./internal/kg -update and review the diff", golden)
	}
	// deterministic: a second build gives the same bytes
	var again bytes.Buffer
	buildFixture(t).JSON(&again)
	if !bytes.Equal(buf.Bytes(), again.Bytes()) {
		t.Error("two builds differ")
	}
}

func TestEdgesAreTraceable(t *testing.T) {
	g := buildFixture(t)
	for _, e := range g.Edges {
		if e.Derivation != Explicit && e.Span == "" {
			t.Errorf("%s edge %s -> %s has no span", e.Derivation, e.From, e.To)
		}
		if e.Derivation == Inferred && (e.Confidence <= 0 || e.Confidence > 1) {
			t.Errorf("inferred edge %s -> %s has confidence %v", e.From, e.To, e.Confidence)
		}
		if e.File == "" && e.Type != EInRepo {
			t.Errorf("%s edge %s -> %s has no file", e.Type, e.From, e.To)
		}
		if g.Node(e.From) == nil || g.Node(e.To) == nil {
			t.Errorf("edge %s -> %s has a dangling end", e.From, e.To)
		}
	}
}

func TestFixtureCoversEdgeTypes(t *testing.T) {
	g := buildFixture(t)
	seen := map[string]bool{}
	for _, e := range g.Edges {
		seen[e.Type] = true
	}
	for _, typ := range []string{ELinksTo, EChildOf, EIncludes, EMentions, EDefines, EDocuments, ETaggedWith, EHasType,
		EForAudience, EInVersion, ESupersedes, EOverlaps, ERelated, EOwnedBy, EInRepo} {
		if !seen[typ] {
			t.Errorf("no %s edge in the fixture graph", typ)
		}
	}
	want := map[string]bool{
		"entity:command:lumen deploy": true, "entity:flag:lumen --force": true, "entity:config-key:server.port": true,
		"entity:api-endpoint:get /v1/status": true, "entity:term:configuration file": true, "entity:term:workspace": true,
	}
	for id := range want {
		if g.Node(id) == nil {
			t.Errorf("missing node %s", id)
		}
	}
	// a broken link is a dashed edge to a missing node
	broken := 0
	for _, e := range g.Edges {
		if e.Broken {
			broken++
			if g.Node(e.To).Type != TMissing {
				t.Errorf("broken edge to %s, want a missing node", e.To)
			}
		}
	}
	if broken != 1 {
		t.Errorf("broken edges = %d, want 1", broken)
	}
}

func TestStatsAndExplain(t *testing.T) {
	g := buildFixture(t)
	st := g.ComputeStats(5)
	if len(st.Orphans) != 1 || st.Orphans[0] != "page:/old-install" {
		t.Errorf("orphans = %v", st.Orphans)
	}
	if len(st.Inconsistent) != 1 || st.Inconsistent[0].ID != "entity:term:configuration file" {
		t.Errorf("inconsistent = %+v", st.Inconsistent)
	}
	x, err := g.Explain("config file") // an alias finds the canonical entity
	if err != nil {
		t.Fatal(err)
	}
	if x.Node.ID != "entity:term:configuration file" || len(x.Defines) != 1 || x.Defines[0].File != "docs/glossary.md" {
		t.Errorf("explain = %+v", x)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	g := buildFixture(t)
	db := filepath.Join(t.TempDir(), "g.db")
	if err := g.Save(db); err != nil {
		t.Fatal(err)
	}
	h, err := Load(db)
	if err != nil {
		t.Fatal(err)
	}
	var a, b bytes.Buffer
	g.JSON(&a)
	h.JSON(&b)
	if a.String() != b.String() {
		t.Error("graph changed after save and load")
	}
}

func TestExportsAndFilters(t *testing.T) {
	g := buildFixture(t)
	for name, f := range map[string]func(*bytes.Buffer) error{
		"graphml": func(w *bytes.Buffer) error { return g.GraphML(w) },
		"jsonld":  func(w *bytes.Buffer) error { return g.JSONLD(w) },
		"cypher":  func(w *bytes.Buffer) error { return g.Cypher(w) },
		"mermaid": func(w *bytes.Buffer) error { return g.Mermaid(w) },
		"dot":     func(w *bytes.Buffer) error { return g.DOT(w) },
		"html":    func(w *bytes.Buffer) error { return g.HTML(w) },
		"mindmap": func(w *bytes.Buffer) error { return g.Mindmap(w, "lumen deploy", 2) },
	} {
		var buf bytes.Buffer
		if err := f(&buf); err != nil || buf.Len() == 0 {
			t.Errorf("%s: %v (%d bytes)", name, err, buf.Len())
		}
		if name == "html" && (strings.Contains(buf.String(), "http://") || strings.Contains(buf.String(), "https://")) && strings.Contains(buf.String(), "<script src") {
			t.Error("viewer must not load remote scripts")
		}
	}
	var mm bytes.Buffer
	g.Mermaid(&mm)
	if !strings.Contains(mm.String(), "-.->") {
		t.Error("broken and inferred edges should be dashed in Mermaid")
	}
	sub, err := g.Subgraph(Filter{Entity: "server.port", Depth: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range sub.Nodes {
		if n.Type == TPage && n.ID != "page:/configure" && n.ID != "page:/reference/config" && n.ID != "page:/troubleshoot" {
			t.Errorf("unexpected page %s around server.port", n.ID)
		}
	}
	sub, _ = g.Subgraph(Filter{Types: []string{"page"}, MaxNodes: 3})
	if len(sub.Nodes) != 3 {
		t.Errorf("max-nodes: got %d", len(sub.Nodes))
	}
	if _, err := g.Subgraph(Filter{Root: "no such thing"}); err == nil {
		t.Error("unknown root should fail")
	}
}
