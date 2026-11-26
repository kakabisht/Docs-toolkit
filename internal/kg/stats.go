package kg

import (
	"sort"
	"strconv"
	"strings"
)

// Stats summarises the graph's shape and its problems.
type Stats struct {
	Nodes        map[string]int `json:"nodes"`
	Edges        map[string]int `json:"edges"`
	Derivations  map[string]int `json:"derivations"`
	Entities     map[string]int `json:"entities_by_kind,omitempty"`
	Orphans      []string       `json:"orphans"`   // pages no other page links to, and not in the navigation
	DeadEnds     []string       `json:"dead_ends"` // pages that link to no other page
	Hubs         []Hub          `json:"hubs"`
	Clusters     ClusterStats   `json:"clusters"`
	Depth        DepthStats     `json:"depth"`
	Broken       []Edge         `json:"broken_edges"`
	Undefined    []EntityUse    `json:"entities_without_definition"` // mentioned, but no page defines or documents them
	MultiDefined []EntityUse    `json:"entities_defined_more_than_once"`
	Inconsistent []Naming       `json:"inconsistent_names"`
	Reviews      []Review       `json:"reviews"`
}

// Hub is a well-connected page.
type Hub struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	In    int    `json:"in"`
	Out   int    `json:"out"`
}

// ClusterStats describes connected groups of pages (links, navigation and
// includes, ignoring direction).
type ClusterStats struct {
	Count    int        `json:"count"`
	Sizes    []int      `json:"sizes"`              // largest first
	Isolated []string   `json:"isolated,omitempty"` // pages in no cluster with another page
	Members  [][]string `json:"-"`
}

// DepthStats is click depth from the entry page.
type DepthStats struct {
	Entry       string      `json:"entry"`
	Max         int         `json:"max"`
	Histogram   map[int]int `json:"histogram"`
	Unreachable []string    `json:"unreachable"`
}

// EntityUse is an entity with the pages that use or define it.
type EntityUse struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Pages []string `json:"pages"`
}

// Naming is an entity written in more than one way.
type Naming struct {
	ID        string         `json:"id"`
	Label     string         `json:"label"`
	Spellings map[string]int `json:"spellings"`
}

// ComputeStats works out Stats. top caps the hub list.
func (g *Graph) ComputeStats(top int) Stats {
	st := Stats{Nodes: map[string]int{}, Edges: map[string]int{}, Derivations: map[string]int{}, Entities: map[string]int{},
		Depth: DepthStats{Histogram: map[int]int{}, Entry: g.Entry}, Reviews: g.Reviews}
	var pages []string
	for _, n := range g.Nodes {
		st.Nodes[n.Type]++
		if n.Type == TEntity {
			st.Entities[n.Kind]++
		}
		if n.Type == TPage {
			pages = append(pages, n.ID)
		}
	}
	sort.Strings(pages)
	in, out := map[string]map[string]bool{}, map[string]map[string]bool{}
	inNav := map[string]bool{}
	adj := map[string]map[string]bool{}
	link := func(a, b string) {
		if adj[a] == nil {
			adj[a] = map[string]bool{}
		}
		if adj[b] == nil {
			adj[b] = map[string]bool{}
		}
		adj[a][b], adj[b][a] = true, true
	}
	for _, e := range g.Edges {
		st.Edges[e.Type]++
		st.Derivations[e.Derivation]++
		if e.Broken {
			st.Broken = append(st.Broken, e)
			continue
		}
		fp, tp := pageOf(e.From), pageOf(e.To)
		if fp == "" || tp == "" || fp == tp {
			if e.Type == EIncludes && fp != "" {
				// pages sharing a partial are connected through it
				link(fp, e.To)
			}
			continue
		}
		switch e.Type {
		case ELinksTo:
			if in[tp] == nil {
				in[tp] = map[string]bool{}
			}
			if out[fp] == nil {
				out[fp] = map[string]bool{}
			}
			in[tp][fp], out[fp][tp] = true, true
			link(fp, tp)
		case EChildOf:
			link(fp, tp)
			if strings.HasPrefix(e.Span, "navigation:") {
				inNav[fp] = true
			}
		}
	}
	var hubs []Hub
	for _, p := range pages {
		if len(in[p]) == 0 && !inNav[p] && g.Node(p).Props["in_nav"] != "true" && p != g.Entry {
			st.Orphans = append(st.Orphans, p)
		}
		if len(out[p]) == 0 {
			st.DeadEnds = append(st.DeadEnds, p)
		}
		hubs = append(hubs, Hub{ID: p, Label: g.Node(p).Label, In: len(in[p]), Out: len(out[p])})
	}
	sort.SliceStable(hubs, func(i, j int) bool {
		a, b := hubs[i].In+hubs[i].Out, hubs[j].In+hubs[j].Out
		if a != b {
			return a > b
		}
		return hubs[i].ID < hubs[j].ID
	})
	if top > 0 && len(hubs) > top {
		hubs = hubs[:top]
	}
	st.Hubs = hubs
	// clusters: connected components over pages
	seen := map[string]bool{}
	for _, p := range pages {
		if seen[p] {
			continue
		}
		var comp []string
		stack := []string{p}
		seen[p] = true
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if strings.HasPrefix(x, "page:") {
				comp = append(comp, x)
			}
			nbs := make([]string, 0, len(adj[x]))
			for nb := range adj[x] {
				nbs = append(nbs, nb)
			}
			sort.Strings(nbs)
			for _, nb := range nbs {
				if !seen[nb] {
					seen[nb] = true
					stack = append(stack, nb)
				}
			}
		}
		sort.Strings(comp)
		if len(comp) == 1 {
			st.Clusters.Isolated = append(st.Clusters.Isolated, comp[0])
			continue
		}
		st.Clusters.Members = append(st.Clusters.Members, comp)
	}
	sort.SliceStable(st.Clusters.Members, func(i, j int) bool { return len(st.Clusters.Members[i]) > len(st.Clusters.Members[j]) })
	st.Clusters.Count = len(st.Clusters.Members)
	for _, m := range st.Clusters.Members {
		st.Clusters.Sizes = append(st.Clusters.Sizes, len(m))
	}
	// depth comes from the page analysis at build time
	for _, p := range pages {
		d, err := strconv.Atoi(g.Node(p).Props["depth"])
		if err != nil {
			continue
		}
		if d < 0 {
			st.Depth.Unreachable = append(st.Depth.Unreachable, p)
			continue
		}
		st.Depth.Histogram[d]++
		st.Depth.Max = max(st.Depth.Max, d)
	}
	// entities
	for _, n := range g.Nodes {
		if n.Type != TEntity {
			continue
		}
		var defs, docs, users []string
		for _, e := range g.In(n.ID) {
			switch e.Type {
			case EDefines:
				defs = appendUnique(defs, e.From)
				docs = appendUnique(docs, e.From)
			case EDocuments:
				docs = appendUnique(docs, e.From)
			case EMentions:
				users = appendUnique(users, e.From)
			}
		}
		if len(docs) == 0 && len(users) > 0 {
			st.Undefined = append(st.Undefined, EntityUse{n.ID, n.Label, users})
		}
		if len(defs) > 1 {
			st.MultiDefined = append(st.MultiDefined, EntityUse{n.ID, n.Label, defs})
		}
		sp := map[string]int{}
		for k, v := range n.Props {
			if s, ok := strings.CutPrefix(k, "spelling:"); ok {
				sp[s], _ = strconv.Atoi(v)
			}
		}
		// case alone (a capital at the start of a sentence or heading) is not inconsistency
		folded := map[string]bool{}
		for k := range sp {
			folded[strings.ToLower(k)] = true
		}
		if len(folded) > 1 || (len(folded) == 1 && !folded[strings.ToLower(n.Label)]) {
			st.Inconsistent = append(st.Inconsistent, Naming{n.ID, n.Label, sp})
		}
	}
	byPages := func(es []EntityUse) {
		sort.SliceStable(es, func(i, j int) bool {
			if len(es[i].Pages) != len(es[j].Pages) {
				return len(es[i].Pages) > len(es[j].Pages)
			}
			return es[i].ID < es[j].ID
		})
	}
	byPages(st.Undefined)
	byPages(st.MultiDefined)
	return st
}

func appendUnique(s []string, v string) []string {
	if in(v, s) {
		return s
	}
	return append(s, v)
}

// pageOf returns the page a page or section node belongs to.
func pageOf(id string) string {
	switch {
	case strings.HasPrefix(id, "page:"):
		return id
	case strings.HasPrefix(id, "section:"):
		key := strings.TrimPrefix(id, "section:")
		if i := strings.LastIndex(key, "#"); i >= 0 {
			key = key[:i]
		}
		return "page:" + key
	}
	return ""
}
