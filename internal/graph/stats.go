package graph

import (
	"sort"
	"strings"
)

// Stats summarizes the graph.
type Stats struct {
	Pages          int            `json:"pages"`
	ErrorPages     int            `json:"error_pages"` // linked URLs that failed to load (live mode)
	Sections       int            `json:"sections"`
	Links          int            `json:"links"`
	InternalLinks  int            `json:"internal_links"`
	ExternalLinks  int            `json:"external_links"`
	AnchorLinks    int            `json:"anchor_links"`
	AssetLinks     int            `json:"asset_links"`
	NavLinks       int            `json:"nav_links"`  // navigation and section-hierarchy edges
	PageEdges      int            `json:"page_edges"` // distinct page->page content links
	Words          int            `json:"words"`
	CodeBlocks     int            `json:"code_blocks"`
	Entry          string         `json:"entry"`
	Orphans        []string       `json:"orphans"`     // no inbound links from other pages
	Unreachable    []string       `json:"unreachable"` // not reachable from the entry page
	DeadEnds       []string       `json:"dead_ends"`   // no outbound links to other pages
	MaxDepth       int            `json:"max_depth"`
	DepthHistogram map[int]int    `json:"depth_histogram"`
	Top            []RankedPage   `json:"top"`
	Hubs           []RankedPage   `json:"hubs"` // most outbound links
	ExternalHosts  []HostCount    `json:"external_hosts"`
	Formats        map[string]int `json:"formats"`
}

// RankedPage is a page with its importance.
type RankedPage struct {
	Key   string  `json:"key"`
	Title string  `json:"title"`
	Rank  float64 `json:"rank"`
	In    int     `json:"in"`
	Out   int     `json:"out"`
}

// HostCount counts links to one external host.
type HostCount struct {
	Host  string `json:"host"`
	Links int    `json:"links"`
}

// ComputeStats summarizes g. top limits the ranked lists.
func (g *Graph) ComputeStats(top int) Stats {
	st := Stats{Entry: g.Entry, DepthHistogram: map[int]int{}, Formats: map[string]int{}}
	hosts := map[string]int{}
	for _, e := range g.Edges {
		if e.Nav {
			st.NavLinks++
			continue
		}
		st.Links++
		switch e.Kind {
		case "internal":
			st.InternalLinks++
		case "external":
			st.ExternalLinks++
			hosts[hostOf(e.URL)]++
		case "anchor":
			st.AnchorLinks++
		case "asset":
			st.AssetLinks++
		}
	}
	for _, tos := range g.Adjacency() {
		st.PageEdges += len(tos)
	}
	for _, n := range g.Nodes {
		if n.Err != "" {
			st.ErrorPages++
			continue
		}
		st.Pages++
		st.Sections += len(n.Sections)
		st.Words += n.Words
		st.CodeBlocks += len(n.Code)
		st.Formats[n.Format]++
		if n.In == 0 && n.Key != g.Entry {
			st.Orphans = append(st.Orphans, n.Key)
		}
		if n.Depth < 0 {
			st.Unreachable = append(st.Unreachable, n.Key)
		} else {
			st.DepthHistogram[n.Depth]++
			if n.Depth > st.MaxDepth {
				st.MaxDepth = n.Depth
			}
		}
		if n.Out == 0 {
			st.DeadEnds = append(st.DeadEnds, n.Key)
		}
	}
	for _, n := range g.TopByRank(0) {
		if len(st.Top) >= top {
			break
		}
		if n.Err != "" {
			continue
		}
		st.Top = append(st.Top, RankedPage{n.Key, n.Title, n.Rank, n.In, n.Out})
	}
	hubs := append([]*Node(nil), g.Nodes...)
	sort.SliceStable(hubs, func(i, j int) bool { return hubs[i].Out > hubs[j].Out })
	for i := 0; i < len(hubs) && i < top; i++ {
		st.Hubs = append(st.Hubs, RankedPage{hubs[i].Key, hubs[i].Title, hubs[i].Rank, hubs[i].In, hubs[i].Out})
	}
	for h, c := range hosts {
		st.ExternalHosts = append(st.ExternalHosts, HostCount{h, c})
	}
	sort.Slice(st.ExternalHosts, func(i, j int) bool {
		if st.ExternalHosts[i].Links != st.ExternalHosts[j].Links {
			return st.ExternalHosts[i].Links > st.ExternalHosts[j].Links
		}
		return st.ExternalHosts[i].Host < st.ExternalHosts[j].Host
	})
	if len(st.ExternalHosts) > top {
		st.ExternalHosts = st.ExternalHosts[:top]
	}
	return st
}

func hostOf(u string) string {
	s := u
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimPrefix(strings.ToLower(s), "www.")
}
