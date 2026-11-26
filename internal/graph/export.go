package graph

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"regexp"
	"sort"
	"strings"
)

// ExportOptions limits what goes into an export.
type ExportOptions struct {
	MaxNodes int  // keep the top-ranked N pages (0 = all)
	Sections bool // include section nodes (mermaid/dot/json)
}

func (g *Graph) subset(max int) (map[string]bool, []*Node) {
	nodes := g.TopByRank(max)
	keep := map[string]bool{}
	for _, n := range nodes {
		keep[n.Key] = true
	}
	return keep, nodes
}

var idSafe = regexp.MustCompile(`[^A-Za-z0-9_]`)

// Mermaid writes a flowchart of pages (and optionally sections).
func (g *Graph) Mermaid(w io.Writer, opt ExportOptions) error {
	keep, nodes := g.subset(opt.MaxNodes)
	ids := map[string]string{}
	for i, n := range nodes {
		ids[n.Key] = fmt.Sprintf("p%d", i)
	}
	fmt.Fprintln(w, "flowchart LR")
	for _, n := range nodes {
		fmt.Fprintf(w, "  %s[\"%s\"]\n", ids[n.Key], mermaidLabel(labelOf(n)))
		if opt.Sections {
			for j, h := range n.Sections {
				if h.Level > 3 {
					continue
				}
				sid := fmt.Sprintf("%s_s%d", ids[n.Key], j)
				fmt.Fprintf(w, "  %s([\"%s\"])\n  %s -.- %s\n", sid, mermaidLabel(h.Text), ids[n.Key], sid)
			}
		}
	}
	adj := g.Adjacency()
	for _, n := range nodes {
		for _, to := range sortedKeys(adj[n.Key]) {
			if keep[to] && to != n.Key {
				fmt.Fprintf(w, "  %s --> %s\n", ids[n.Key], ids[to])
			}
		}
	}
	return nil
}

func mermaidLabel(s string) string {
	s = strings.ReplaceAll(s, `"`, "#quot;")
	return strings.NewReplacer("[", "(", "]", ")", "\n", " ").Replace(s)
}

func labelOf(n *Node) string {
	if n.Title != "" {
		return n.Title
	}
	return n.Key
}

// DOT writes a Graphviz digraph.
func (g *Graph) DOT(w io.Writer, opt ExportOptions) error {
	keep, nodes := g.subset(opt.MaxNodes)
	fmt.Fprintln(w, "digraph docs {\n  rankdir=LR;\n  node [shape=box, style=rounded];")
	for _, n := range nodes {
		fmt.Fprintf(w, "  %q [label=%q, tooltip=%q];\n", n.Key, labelOf(n), n.Key)
		if opt.Sections {
			for _, h := range n.Sections {
				if h.Level <= 3 {
					sid := n.Key + "#" + h.Anchor
					fmt.Fprintf(w, "  %q [label=%q, shape=note];\n  %q -> %q [style=dashed, arrowhead=none];\n", sid, h.Text, n.Key, sid)
				}
			}
		}
	}
	adj := g.Adjacency()
	for _, n := range nodes {
		for _, to := range sortedKeys(adj[n.Key]) {
			if keep[to] && to != n.Key {
				fmt.Fprintf(w, "  %q -> %q;\n", n.Key, to)
			}
		}
	}
	fmt.Fprintln(w, "}")
	return nil
}

type jsonNode struct {
	ID    string  `json:"id"`
	Type  string  `json:"type"`
	Label string  `json:"label"`
	Page  string  `json:"page,omitempty"`
	URL   string  `json:"url,omitempty"`
	Rank  float64 `json:"rank,omitempty"`
	Depth int     `json:"depth"`
	In    int     `json:"in"`
	Out   int     `json:"out"`
	Words int     `json:"words,omitempty"`
	Level int     `json:"level,omitempty"`
}

type jsonEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
	Count  int    `json:"count,omitempty"`
}

// JSON writes a node-link graph (the d3/networkx node_link format).
func (g *Graph) JSON(w io.Writer, opt ExportOptions) error {
	keep, nodes := g.subset(opt.MaxNodes)
	out := struct {
		Directed bool       `json:"directed"`
		Source   string     `json:"source"`
		Entry    string     `json:"entry"`
		Nodes    []jsonNode `json:"nodes"`
		Links    []jsonEdge `json:"links"`
	}{Directed: true, Source: g.Source, Entry: g.Entry}
	for _, n := range nodes {
		out.Nodes = append(out.Nodes, jsonNode{ID: n.Key, Type: "page", Label: labelOf(n), URL: n.URL, Rank: n.Rank, Depth: n.Depth, In: n.In, Out: n.Out, Words: n.Words})
		if opt.Sections {
			for _, h := range n.Sections {
				id := n.Key + "#" + h.Anchor
				out.Nodes = append(out.Nodes, jsonNode{ID: id, Type: "section", Label: h.Text, Page: n.Key, Level: h.Level, Depth: -1})
				out.Links = append(out.Links, jsonEdge{Source: n.Key, Target: id, Type: "contains"})
			}
		}
	}
	counts := map[[2]string]int{}
	for _, e := range g.Edges {
		if e.Kind == "internal" && e.Exists && keep[e.From] && keep[e.To] && e.From != e.To && !e.Image {
			counts[[2]string{e.From, e.To}]++
		}
	}
	var pairs [][2]string
	for p := range counts {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0]+"\x00"+pairs[i][1] < pairs[j][0]+"\x00"+pairs[j][1] })
	for _, p := range pairs {
		out.Links = append(out.Links, jsonEdge{Source: p[0], Target: p[1], Type: "links_to", Count: counts[p]})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

//go:embed viewer.html
var viewerHTML string

var viewer = template.Must(template.New("viewer").Parse(viewerHTML))

// HTML writes a self-contained interactive viewer (no external scripts).
func (g *Graph) HTML(w io.Writer, opt ExportOptions) error {
	var buf strings.Builder
	if err := g.JSON(&buf, ExportOptions{MaxNodes: opt.MaxNodes}); err != nil {
		return err
	}
	st := g.ComputeStats(10)
	return viewer.Execute(w, map[string]any{
		"Title": g.Source,
		"Data":  template.JS(buf.String()),
		"Stats": st,
	})
}

// WriteLLMSTxt writes an llms.txt index (https://llmstxt.org) generated from the
// graph: pages grouped by top-level section, ordered by importance.
func (g *Graph) WriteLLMSTxt(w io.Writer, title string) error {
	if title == "" {
		title = g.Source
		if e := g.Node(g.Entry); e != nil && e.Title != "" {
			title = e.Title
		}
	}
	fmt.Fprintf(w, "# %s\n\n", title)
	if e := g.Node(g.Entry); e != nil && e.Description != "" {
		fmt.Fprintf(w, "> %s\n\n", e.Description)
	}
	groups := map[string][]*Node{}
	var order []string
	for _, n := range g.TopByRank(0) {
		if n.Err != "" || n.Draft {
			continue
		}
		grp := sectionOf(n.Key)
		if _, ok := groups[grp]; !ok {
			order = append(order, grp)
		}
		groups[grp] = append(groups[grp], n)
	}
	for _, grp := range order {
		name := grp
		if p := g.Node(grp); p != nil && p.Title != "" {
			name = p.Title
		} else if name == "" || name == "/" {
			name = "Docs"
		} else {
			name = titleCase(strings.ReplaceAll(name[strings.LastIndex(name, "/")+1:], "-", " "))
		}
		fmt.Fprintf(w, "## %s\n\n", name)
		for _, n := range groups[grp] {
			link := n.URL
			if n.FinalURL != "" {
				link = n.FinalURL
			}
			if link == "" {
				link = n.Key
			}
			line := fmt.Sprintf("- [%s](%s)", labelOf(n), link)
			if n.Description != "" {
				line += ": " + strings.Join(strings.Fields(n.Description), " ")
			}
			fmt.Fprintln(w, line)
		}
		fmt.Fprintln(w)
	}
	return nil
}

// sectionOf returns the first path segment of a page key, keeping any host.
func sectionOf(key string) string {
	host := ""
	p := key
	if i := strings.Index(key, "/"); i > 0 {
		host, p = key[:i], key[i:]
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	if len(segs) <= 1 {
		return host + "/"
	}
	return host + "/" + segs[0]
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
