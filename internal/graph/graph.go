// Package graph turns a crawled Site into a knowledge graph of pages,
// sections and links, ranks pages, and stores the result in SQLite.
package graph

import (
	"path"
	"sort"
	"time"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/model"
)

// Node is a docs page.
type Node struct {
	Key         string            `json:"key"`
	URL         string            `json:"url,omitempty"`
	Path        string            `json:"path,omitempty"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Format      string            `json:"format"`
	Source      string            `json:"source"`
	Words       int               `json:"words"`
	Chars       int               `json:"chars"`
	Status      int               `json:"status,omitempty"`
	FinalURL    string            `json:"final_url,omitempty"`
	Draft       bool              `json:"draft,omitempty"`
	Err         string            `json:"error,omitempty"`
	Rank        float64           `json:"rank"`  // PageRank, scaled so the top page is 1
	Depth       int               `json:"depth"` // clicks from the entry page, -1 if unreachable
	In          int               `json:"in"`    // distinct pages linking here (content or navigation)
	Out         int               `json:"out"`   // distinct pages linked to
	Sections    []model.Heading   `json:"sections"`
	Code        []model.CodeBlock `json:"code_blocks,omitempty"`
	Anchors     map[string]bool   `json:"-"`
	InLLMSTxt   bool              `json:"in_llms_txt"`
	InSitemap   bool              `json:"in_sitemap,omitempty"`
}

// Edge is one link occurrence.
type Edge struct {
	From     string `json:"from"`
	To       string `json:"to,omitempty"` // page key for internal/anchor links
	Section  string `json:"section,omitempty"`
	Line     int    `json:"line,omitempty"`
	Href     string `json:"href"`
	Text     string `json:"text,omitempty"`
	Kind     string `json:"kind"`
	URL      string `json:"url,omitempty"`
	Fragment string `json:"fragment,omitempty"`
	Exists   bool   `json:"exists"`
	ViaAlias bool   `json:"via_alias,omitempty"`
	Image    bool   `json:"image,omitempty"`
	Nav      bool   `json:"nav,omitempty"` // navigation link or generated section hierarchy
}

// Graph is the knowledge graph of one docs source.
type Graph struct {
	Mode    string        `json:"mode"`
	Source  string        `json:"source"`
	BaseURL string        `json:"base_url,omitempty"`
	Built   time.Time     `json:"built"`
	Entry   string        `json:"entry"`
	LLMSTxt model.LLMSTxt `json:"llms_txt"`
	Nodes   []*Node       `json:"nodes"`
	Edges   []Edge        `json:"edges"`

	byKey map[string]*Node
}

// Node returns the node with key k.
func (g *Graph) Node(k string) *Node {
	if g.byKey == nil {
		g.byKey = map[string]*Node{}
		for _, n := range g.Nodes {
			g.byKey[n.Key] = n
		}
	}
	return g.byKey[k]
}

// Build creates the graph from a crawled site.
func Build(s *crawl.Site) *Graph {
	g := &Graph{Mode: string(s.Mode), Source: s.Source, Built: time.Now().UTC(), LLMSTxt: s.LLMSTxt}
	if s.Base != nil {
		g.BaseURL = s.Base.String()
	}
	inLLMS := map[string]bool{}
	for _, h := range s.LLMSTxt.Links {
		if s.Mode == crawl.Local || len(s.Pages) == 0 {
			// resolve llms.txt links as if written on the site root
			t := s.Resolve(&model.Page{Key: "/", Path: "llms.txt"}, h)
			if t.Page != nil {
				inLLMS[t.Page.Key] = true
			}
			continue
		}
		t := s.Resolve(&model.Page{Key: "/", URL: s.LLMSTxt.Location, FinalURL: s.LLMSTxt.Location}, h)
		if t.Page != nil {
			inLLMS[t.Page.Key] = true
		}
	}
	inSitemap := map[string]bool{}
	for _, u := range s.Sitemap {
		t := s.Resolve(&model.Page{Key: "/", URL: s.Base.String(), FinalURL: s.Base.String()}, u)
		if t.Page != nil {
			inSitemap[t.Page.Key] = true
		}
	}
	for _, p := range s.Pages {
		g.Nodes = append(g.Nodes, &Node{
			Key: p.Key, URL: p.URL, Path: p.Path, Title: p.Title, Description: p.Description,
			Format: p.Format, Source: p.Source, Words: p.Words, Chars: p.Chars, Status: p.Status,
			FinalURL: p.FinalURL, Draft: p.Draft, Err: p.Err, Sections: p.Headings, Code: p.CodeBlocks,
			Anchors: p.Anchors, InLLMSTxt: inLLMS[p.Key], InSitemap: inSitemap[p.Key],
		})
		for _, l := range p.Links {
			t := s.Resolve(p, l.Href)
			e := Edge{From: p.Key, Section: l.Section, Line: l.Line, Href: l.Href, Text: l.Text, Kind: string(t.Kind),
				URL: t.URL, Fragment: t.Fragment, Exists: t.Exists, ViaAlias: t.ViaAlias, Image: l.Image, Nav: l.Nav}
			if l.Nav && (t.Kind != crawl.KindInternal || t.Fragment != "") {
				continue // external and anchor links in site chrome are noise
			}
			if t.Kind == crawl.KindInternal || t.Kind == crawl.KindAnchor {
				e.To = t.Key
			}
			g.Edges = append(g.Edges, e)
		}
	}
	if s.Mode == crawl.Local {
		g.Edges = append(g.Edges, hierarchyEdges(s)...)
	}
	g.Entry = pickEntry(g, s)
	g.Analyze()
	return g
}

// hierarchyEdges links each section index to the pages inside it, standing in
// for the sidebar and section listings a generator builds from the folder tree.
func hierarchyEdges(s *crawl.Site) []Edge {
	index := map[string]string{} // dir -> index page key
	for _, p := range s.Pages {
		if p.Path != "" && crawl.IsIndexFile(p.Path) {
			index[path.Dir(p.Path)] = p.Key
		}
	}
	var out []Edge
	for _, p := range s.Pages {
		if p.Path == "" {
			continue
		}
		dir := path.Dir(p.Path)
		if crawl.IsIndexFile(p.Path) {
			if dir == "." {
				continue
			}
			dir = path.Dir(dir)
		}
		// only the folder's own index lists it; a folder without an index
		// usually gets no listing, so its pages stay unlinked
		if k, ok := index[dir]; ok && k != p.Key {
			out = append(out, Edge{From: k, To: p.Key, Kind: "internal", Href: "(section listing)", Exists: true, Nav: true})
		}
	}
	return out
}

func pickEntry(g *Graph, s *crawl.Site) string {
	if s.Mode == crawl.Live && s.Base != nil {
		k := crawl.NormalizeURL(s.Base)
		if g.Node(k) != nil {
			return k
		}
	}
	if g.Node("/") != nil {
		return "/"
	}
	// fall back to the shortest key
	best := ""
	for _, n := range g.Nodes {
		if best == "" || len(n.Key) < len(best) {
			best = n.Key
		}
	}
	return best
}

// Analyze computes in/out degree, click depth and PageRank.
func (g *Graph) Analyze() {
	g.byKey = nil
	adj := g.Adjacency()
	nav := g.NavAdjacency()
	// inbound counts navigation too: a page reachable from a sidebar or section
	// listing is not an orphan. PageRank and out-degree use content links only.
	in := map[string]int{}
	for from, tos := range nav {
		for to := range tos {
			if to != from {
				in[to]++
			}
		}
	}
	for _, n := range g.Nodes {
		n.In = in[n.Key]
		n.Out = 0
		for to := range adj[n.Key] {
			if to != n.Key {
				n.Out++
			}
		}
		n.Depth = -1
	}
	// BFS for click depth
	if e := g.Node(g.Entry); e != nil {
		e.Depth = 0
		q := []string{e.Key}
		for len(q) > 0 {
			cur := q[0]
			q = q[1:]
			d := g.Node(cur).Depth
			for _, to := range sortedKeys(nav[cur]) {
				if n := g.Node(to); n != nil && n.Depth < 0 {
					n.Depth = d + 1
					q = append(q, to)
				}
			}
		}
	}
	var ok []*Node
	for _, n := range g.Nodes {
		if n.Err == "" {
			ok = append(ok, n)
		}
	}
	pr := PageRank(ok, adj, 0.85, 50)
	for _, n := range g.Nodes {
		n.Rank = pr[n.Key]
	}
}

// Adjacency returns distinct page->page content links between existing pages.
func (g *Graph) Adjacency() map[string]map[string]bool { return g.adjacency(false) }

// NavAdjacency is Adjacency plus navigation and section-hierarchy edges.
func (g *Graph) NavAdjacency() map[string]map[string]bool { return g.adjacency(true) }

func (g *Graph) adjacency(withNav bool) map[string]map[string]bool {
	adj := map[string]map[string]bool{}
	for _, e := range g.Edges {
		if e.Kind != "internal" || !e.Exists || e.To == "" || e.Image || (e.Nav && !withNav) {
			continue
		}
		if n := g.Node(e.To); n == nil || n.Err != "" {
			continue
		}
		if adj[e.From] == nil {
			adj[e.From] = map[string]bool{}
		}
		adj[e.From][e.To] = true
	}
	return adj
}

// PageRank runs the power iteration and scales results so the top page scores 1.
func PageRank(nodes []*Node, adj map[string]map[string]bool, d float64, iters int) map[string]float64 {
	n := float64(len(nodes))
	pr := map[string]float64{}
	if n == 0 {
		return pr
	}
	for _, nd := range nodes {
		pr[nd.Key] = 1 / n
	}
	for i := 0; i < iters; i++ {
		next := map[string]float64{}
		dangling := 0.0
		for _, nd := range nodes {
			outs := adj[nd.Key]
			cnt := 0
			for to := range outs {
				if to != nd.Key {
					if _, ok := pr[to]; ok {
						cnt++
					}
				}
			}
			if cnt == 0 {
				dangling += pr[nd.Key]
				continue
			}
			share := pr[nd.Key] / float64(cnt)
			for to := range outs {
				if to != nd.Key {
					if _, ok := pr[to]; ok {
						next[to] += share
					}
				}
			}
		}
		for _, nd := range nodes {
			next[nd.Key] = (1-d)/n + d*(next[nd.Key]+dangling/n)
		}
		pr = next
	}
	maxv := 0.0
	for _, v := range pr {
		if v > maxv {
			maxv = v
		}
	}
	if maxv > 0 {
		for k, v := range pr {
			pr[k] = v / maxv
		}
	}
	return pr
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TopByRank returns the n highest ranked nodes.
func (g *Graph) TopByRank(n int) []*Node {
	nodes := append([]*Node(nil), g.Nodes...)
	sort.SliceStable(nodes, func(i, j int) bool { return nodes[i].Rank > nodes[j].Rank })
	if n > 0 && len(nodes) > n {
		nodes = nodes[:n]
	}
	return nodes
}
