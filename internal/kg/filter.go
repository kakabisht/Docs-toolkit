package kg

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Filter selects part of the graph for viewing or export.
type Filter struct {
	Types    []string // node types to keep (page, section, entity, tag, …); entity kinds also work
	Section  string   // keep pages whose key starts with this path, and what they touch
	Tag      string   // keep pages with this tag, and what they touch
	Entity   string   // keep this entity and the pages around it
	Root     string   // start from this node (ID, label or alias)
	Depth    int      // hops from the seeds (default 1, or 2 with Root)
	MaxNodes int      // keep the most connected nodes, seeds first
	Inferred bool     // include inferred edges (related-to, overlaps-with, inferred has-type)
}

// Empty reports whether the filter selects the whole graph.
func (f Filter) Empty() bool {
	return len(f.Types) == 0 && f.Section == "" && f.Tag == "" && f.Entity == "" && f.Root == "" && f.MaxNodes == 0
}

// Find resolves a query to a node: exact ID, then label or alias (case
// insensitive), preferring entities, then pages.
func (g *Graph) Find(q string) (*Node, []*Node) {
	if n := g.Node(q); n != nil {
		return n, nil
	}
	lq := strings.ToLower(strings.Trim(q, "` "))
	var hits []*Node
	for _, n := range g.Nodes {
		if strings.ToLower(n.Label) == lq || strings.TrimPrefix(n.ID, n.Type+":") == lq {
			hits = append(hits, n)
			continue
		}
		for _, a := range n.Aliases {
			if strings.ToLower(a) == lq {
				hits = append(hits, n)
				break
			}
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}
	rank := map[string]int{TEntity: 0, TPage: 1, TTag: 2, TSection: 3}
	sort.SliceStable(hits, func(i, j int) bool {
		ri, ok := rank[hits[i].Type]
		if !ok {
			ri = 9
		}
		rj, ok := rank[hits[j].Type]
		if !ok {
			rj = 9
		}
		if ri != rj {
			return ri < rj
		}
		return hits[i].ID < hits[j].ID
	})
	return hits[0], hits[1:]
}

// Subgraph returns a new graph with the nodes the filter selects and the
// edges between them. It returns an error when a named root, tag or entity
// is not in the graph.
func (g *Graph) Subgraph(f Filter) (*Graph, error) {
	edges := g.Edges
	if !f.Inferred {
		edges = nil
		for _, e := range g.Edges {
			if e.Derivation != Inferred || e.Type == EHasType {
				edges = append(edges, e)
			}
		}
	}
	keep := map[string]bool{}
	var seeds []string
	seeded := false
	if f.Root != "" {
		seeded = true
		n, _ := g.Find(f.Root)
		if n == nil {
			return nil, fmt.Errorf("no node matches %q", f.Root)
		}
		seeds = append(seeds, n.ID)
	}
	if f.Entity != "" {
		seeded = true
		n, _ := g.Find(f.Entity)
		if n == nil || n.Type != TEntity {
			return nil, fmt.Errorf("no entity matches %q", f.Entity)
		}
		seeds = append(seeds, n.ID)
	}
	if f.Tag != "" {
		seeded = true
		id := TagID(f.Tag)
		if n, _ := g.Find(f.Tag); n != nil && n.Type == TTag {
			id = n.ID
		}
		if g.Node(id) == nil {
			return nil, fmt.Errorf("no tag %q", f.Tag)
		}
		seeds = append(seeds, id)
		for _, e := range edges {
			if e.To == id && e.Type == ETaggedWith {
				seeds = append(seeds, e.From)
			}
		}
	}
	if f.Section != "" {
		seeded = true
		prefix := strings.Trim(f.Section, "/")
		for _, n := range g.Nodes {
			if n.Type == TPage {
				key := strings.TrimPrefix(n.ID, "page:")
				if key == prefix || strings.HasPrefix(key, prefix+"/") || (prefix == "" && key != "") {
					seeds = append(seeds, n.ID)
				}
			}
		}
		if len(seeds) == 0 {
			return nil, fmt.Errorf("no pages under %q", f.Section)
		}
	}
	depth := f.Depth
	if depth == 0 {
		depth = 1
		if f.Root != "" {
			depth = 2
		}
	}
	typeOK := func(n *Node) bool {
		if len(f.Types) == 0 || n == nil {
			return true
		}
		return in(n.Type, f.Types) || (n.Kind != "" && in(n.Kind, f.Types))
	}
	if !seeded {
		for _, n := range g.Nodes {
			if typeOK(n) {
				keep[n.ID] = true
			}
		}
	} else {
		adj := map[string][]string{}
		for _, e := range edges {
			adj[e.From] = append(adj[e.From], e.To)
			adj[e.To] = append(adj[e.To], e.From)
		}
		frontier := seeds
		for _, s := range seeds {
			keep[s] = true
		}
		for d := 0; d < depth; d++ {
			var next []string
			for _, id := range frontier {
				for _, nb := range adj[id] {
					if !keep[nb] {
						keep[nb] = true
						next = append(next, nb)
					}
				}
			}
			frontier = next
		}
		for id := range keep {
			if !typeOK(g.Node(id)) && !in(id, seeds) {
				delete(keep, id)
			}
		}
	}
	if f.MaxNodes > 0 && len(keep) > f.MaxNodes {
		deg := map[string]int{}
		for _, e := range edges {
			if keep[e.From] && keep[e.To] {
				deg[e.From]++
				deg[e.To]++
			}
		}
		isSeed := map[string]bool{}
		for _, s := range seeds {
			isSeed[s] = true
		}
		ids := make([]string, 0, len(keep))
		for id := range keep {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			a, b := ids[i], ids[j]
			if isSeed[a] != isSeed[b] {
				return isSeed[a]
			}
			if deg[a] != deg[b] {
				return deg[a] > deg[b]
			}
			ra, rb := g.rank(a), g.rank(b)
			if ra != rb {
				return ra > rb
			}
			return a < b
		})
		keep = map[string]bool{}
		for _, id := range ids[:f.MaxNodes] {
			keep[id] = true
		}
	}
	out := &Graph{Source: g.Source, Entry: g.Entry, Meta: g.Meta}
	for _, n := range g.Nodes {
		if keep[n.ID] {
			out.Nodes = append(out.Nodes, n)
		}
	}
	for _, e := range edges {
		if keep[e.From] && keep[e.To] {
			out.Edges = append(out.Edges, e)
		}
	}
	for _, r := range g.Reviews {
		all := true
		for _, id := range r.Nodes {
			all = all && keep[id]
		}
		if all {
			out.Reviews = append(out.Reviews, r)
		}
	}
	out.reindex()
	return out, nil
}

func (g *Graph) rank(id string) float64 {
	n := g.Node(id)
	if n == nil || n.Props == nil {
		return 0
	}
	r, _ := strconv.ParseFloat(n.Props["rank"], 64)
	return r
}
