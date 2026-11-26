// Package links finds broken links in a docs graph: missing pages, missing
// section anchors, redirects, missing assets and dead external URLs.
package links

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/docsbot/docsbot/internal/fetch"
	"github.com/docsbot/docsbot/internal/graph"
)

// Problem kinds, in rough order of severity.
const (
	Broken       = "broken"        // target page or URL does not exist (4xx/5xx)
	BrokenAnchor = "broken-anchor" // page exists but the #section does not
	Unreachable  = "unreachable"   // network error, DNS failure or timeout
	Blocked      = "blocked"       // server refused an automated client (401/403/999); verify by hand
	Redirect     = "redirect"      // works, but through a redirect; link to the final URL
)

// Issue is one problem target, with every place that links to it.
type Issue struct {
	Problem  string       `json:"problem"`
	Kind     string       `json:"kind"` // internal | anchor | asset | external
	Target   string       `json:"target"`
	Status   int          `json:"status,omitempty"`
	Detail   string       `json:"detail,omitempty"`
	FinalURL string       `json:"final_url,omitempty"`
	Impact   float64      `json:"impact"` // sum of the linking pages' PageRank
	Sources  []Occurrence `json:"sources"`
}

// Occurrence is where a link appears.
type Occurrence struct {
	Page string `json:"page"`
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
	Line int    `json:"line,omitempty"`
	Href string `json:"href"`
	Text string `json:"text,omitempty"`
}

// Options configures Check.
type Options struct {
	External  bool
	Redirects bool             // report redirects (internal always, external when true)
	Exclude   []*regexp.Regexp // URLs never fetched
	Ignore    []*regexp.Regexp // raw hrefs skipped entirely
	Workers   int
	Progress  func(done, total int)
}

// Report is the result of a check.
type Report struct {
	Source  string         `json:"source"`
	Pages   int            `json:"pages"`
	Checked map[string]int `json:"checked"` // distinct targets checked per kind
	Issues  []Issue        `json:"issues"`
	Counts  map[string]int `json:"counts"` // issues per problem
	Skipped int            `json:"skipped"`
}

type occKey struct{ problem, kind, target string }

// Check runs every link check. client may be nil when External is false and
// the graph is local.
func Check(ctx context.Context, g *graph.Graph, client *fetch.Client, opt Options) *Report {
	pages := 0
	for _, n := range g.Nodes {
		if n.Err == "" {
			pages++
		}
	}
	rep := &Report{Source: g.Source, Pages: pages, Checked: map[string]int{}, Counts: map[string]int{}}
	issues := map[occKey]*Issue{}
	add := func(problem, kind, target string, e graph.Edge, fill func(*Issue)) {
		k := occKey{problem, kind, target}
		is := issues[k]
		if is == nil {
			is = &Issue{Problem: problem, Kind: kind, Target: target}
			if fill != nil {
				fill(is)
			}
			issues[k] = is
		}
		for _, o := range is.Sources {
			if o.Page == e.From && o.Href == e.Href && o.Line == e.Line {
				return // same link repeated on one page/line
			}
		}
		src := g.Node(e.From)
		o := Occurrence{Page: e.From, Line: e.Line, Href: e.Href, Text: e.Text}
		if src != nil {
			o.Path, o.URL = src.Path, src.URL
		}
		is.Sources = append(is.Sources, o)
	}
	excluded := func(u string) bool {
		for _, re := range opt.Exclude {
			if re.MatchString(u) {
				return true
			}
		}
		return false
	}

	// Pass 1: everything the graph already knows.
	remote := map[string][]graph.Edge{} // URLs needing HTTP checks
	remoteKind := map[string]string{}
	seen := map[string]map[string]bool{}
	mark := func(kind, t string) {
		if seen[kind] == nil {
			seen[kind] = map[string]bool{}
		}
		seen[kind][t] = true
	}
	ignored := func(h string) bool {
		for _, re := range opt.Ignore {
			if re.MatchString(h) {
				return true
			}
		}
		return false
	}
	for _, e := range g.Edges {
		if ignored(e.Href) {
			rep.Skipped++
			continue
		}
		switch e.Kind {
		case "other":
			rep.Skipped++
		case "anchor", "internal":
			if e.Exists {
				target := g.Node(e.To)
				mark("internal", e.To)
				if target == nil {
					continue
				}
				if target.Err != "" {
					add(Broken, e.Kind, displayTarget(g, e), e, func(is *Issue) { is.Status, is.Detail = target.Status, target.Err })
					continue
				}
				if e.Fragment != "" && !hasAnchor(target, e.Fragment) {
					t := displayTarget(g, e) + "#" + e.Fragment
					add(BrokenAnchor, e.Kind, t, e, func(is *Issue) { is.Detail = "no section with this id on " + labelFor(target) })
				}
				if e.Kind == "internal" && e.ViaAlias {
					final := target.Key
					if target.FinalURL != "" {
						final = target.FinalURL
					} else if target.URL != "" {
						final = target.URL
					}
					from := e.Href
					if g.Mode == "live" && e.URL != "" {
						from = e.URL
					}
					add(Redirect, e.Kind, from, e, func(is *Issue) {
						is.FinalURL = final
						is.Detail = "redirects or aliases to the canonical page"
					})
				}
				continue
			}
			if g.Mode == "live" && e.URL != "" {
				if excluded(e.URL) {
					rep.Skipped++
					continue
				}
				remote[e.URL] = append(remote[e.URL], e)
				remoteKind[e.URL] = e.Kind
				continue
			}
			mark("internal", e.To)
			add(Broken, e.Kind, displayTarget(g, e), e, func(is *Issue) { is.Detail = "no page at this path" })
		case "asset":
			if e.Exists {
				mark("asset", e.URL)
				continue
			}
			if g.Mode == "live" && e.URL != "" {
				if excluded(e.URL) {
					rep.Skipped++
					continue
				}
				remote[e.URL] = append(remote[e.URL], e)
				remoteKind[e.URL] = "asset"
				continue
			}
			mark("asset", e.URL)
			add(Broken, "asset", e.URL, e, func(is *Issue) { is.Detail = "file not found" })
		case "external":
			if !opt.External || excluded(e.URL) {
				rep.Skipped++
				continue
			}
			remote[e.URL] = append(remote[e.URL], e)
			remoteKind[e.URL] = "external"
		}
	}

	// Pass 2: HTTP checks with a worker pool.
	if len(remote) > 0 && client != nil {
		urls := make([]string, 0, len(remote))
		for u := range remote {
			urls = append(urls, u)
		}
		sort.Strings(urls)
		results := make([]*fetch.Response, len(urls))
		workers := opt.Workers
		if workers <= 0 {
			workers = 16
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		done := 0
		jobs := make(chan int)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					results[i] = client.Check(ctx, urls[i])
					mu.Lock()
					done++
					d := done
					mu.Unlock()
					if opt.Progress != nil {
						opt.Progress(d, len(urls))
					}
				}
			}()
		}
		for i := range urls {
			if ctx.Err() != nil {
				break
			}
			jobs <- i
		}
		close(jobs)
		wg.Wait()
		for i, u := range urls {
			r := results[i]
			kind := remoteKind[u]
			mark(kind, u)
			if r == nil {
				continue
			}
			for _, e := range remote[u] {
				switch {
				case r.Status == 0:
					add(Unreachable, kind, u, e, func(is *Issue) { is.Detail = r.Err })
				case r.Status == 429:
					// rate limited: we can't tell, don't cry wolf
					rep.Skipped++
				case kind == "external" && (r.Status == 401 || r.Status == 403 || r.Status == 999):
					add(Blocked, kind, u, e, func(is *Issue) {
						is.Status = r.Status
						is.Detail = "the server refused an automated request; check it in a browser"
					})
				case r.Status >= 400:
					add(Broken, kind, u, e, func(is *Issue) { is.Status = r.Status; is.FinalURL = finalIfMoved(r) })
				case r.Redirected() && (kind != "external" || opt.Redirects) && !trivialRedirect(u, r.FinalURL):
					add(Redirect, kind, u, e, func(is *Issue) { is.Status = r.Chain[0].Status; is.FinalURL = r.FinalURL })
				}
			}
		}
	}

	for kind, m := range seen {
		rep.Checked[kind] = len(m)
	}
	for _, is := range issues {
		is.Impact = impact(g, is)
		rep.Issues = append(rep.Issues, *is)
		rep.Counts[is.Problem]++
	}
	sort.SliceStable(rep.Issues, func(i, j int) bool {
		a, b := rep.Issues[i], rep.Issues[j]
		if sa, sb := severity(a.Problem), severity(b.Problem); sa != sb {
			return sa < sb
		}
		if a.Impact != b.Impact {
			return a.Impact > b.Impact
		}
		return a.Target < b.Target
	})
	return rep
}

// impact sums the PageRank of the distinct pages that carry the bad link:
// a broken link on the home page hurts more than one on a leaf page.
func impact(g *graph.Graph, is *Issue) float64 {
	seen := map[string]bool{}
	v := 0.0
	for _, o := range is.Sources {
		if seen[o.Page] {
			continue
		}
		seen[o.Page] = true
		if n := g.Node(o.Page); n != nil {
			v += n.Rank
		}
	}
	return v
}

func severity(p string) int {
	switch p {
	case Broken:
		return 0
	case BrokenAnchor:
		return 1
	case Unreachable:
		return 2
	case Blocked:
		return 3
	}
	return 4
}

func hasAnchor(n *graph.Node, frag string) bool {
	if n.Anchors[frag] {
		return true
	}
	if f, err := url.PathUnescape(frag); err == nil && n.Anchors[f] {
		return true
	}
	// GitHub and many renderers lowercase anchors
	if n.Anchors[strings.ToLower(frag)] {
		return true
	}
	// "#top" and empty-ish fragments always work in browsers
	return frag == "top" || frag == "!"
}

func displayTarget(g *graph.Graph, e graph.Edge) string {
	if e.Kind == "anchor" {
		if n := g.Node(e.From); n != nil && g.Mode == "live" && n.URL != "" {
			if n.FinalURL != "" {
				return n.FinalURL
			}
			return n.URL
		}
		return e.From
	}
	if n := g.Node(e.To); n != nil && n.URL != "" && g.Mode == "live" {
		return n.URL
	}
	if e.To != "" {
		return e.To
	}
	return e.URL
}

func labelFor(n *graph.Node) string {
	if n.Title != "" {
		return n.Title
	}
	return n.Key
}

func finalIfMoved(r *fetch.Response) string {
	if r.Redirected() {
		return r.FinalURL
	}
	return ""
}

// trivialRedirect ignores http->https, trailing-slash and www redirects on
// external links, which are noise rather than rot.
func trivialRedirect(from, to string) bool {
	a, err1 := url.Parse(from)
	b, err2 := url.Parse(to)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(strings.TrimPrefix(a.Host, "www."), strings.TrimPrefix(b.Host, "www.")) &&
		strings.TrimSuffix(a.Path, "/") == strings.TrimSuffix(b.Path, "/") && a.RawQuery == b.RawQuery
}

// Failed reports whether rep contains any problem in failOn.
func (rep *Report) Failed(failOn []string) bool {
	for _, p := range failOn {
		if rep.Counts[p] > 0 {
			return true
		}
	}
	return false
}
