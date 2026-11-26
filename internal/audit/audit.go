// Package audit scores the structure and findability of a docs set with
// transparent, deterministic rules. Scores are indicators for prioritising
// work, not measured user outcomes: every score says how it was computed,
// and every item points at a file and line.
package audit

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/doctype"
	"github.com/docsbot/docsbot/internal/kg"
	"github.com/docsbot/docsbot/internal/model"
	"github.com/docsbot/docsbot/internal/search"
	"github.com/docsbot/docsbot/internal/vocab"
)

// Parts of the audit.
const (
	Structure   = "structure"
	Findability = "findability"
)

// Template is what a page of one content type must contain.
type Template struct {
	RequiredHeadings []string `yaml:"required_headings" json:"required_headings,omitempty"` // regexes, case-insensitive
	Steps            bool     `yaml:"steps" json:"steps,omitempty"`                         // a numbered list or step headings
	Structured       bool     `yaml:"structured" json:"structured,omitempty"`               // a table, definition list or code block
	MinLinks         int      `yaml:"min_links" json:"min_links,omitempty"`                 // links to other pages
}

// DefaultTemplates are deliberately light; replace them in the config.
var DefaultTemplates = map[string]Template{
	doctype.HowTo:     {Steps: true},
	doctype.Tutorial:  {Steps: true, RequiredHeadings: []string{`prerequisites|before you (begin|start)|requirements|what you('ll)? need`}},
	doctype.Reference: {Structured: true},
	doctype.Landing:   {MinLinks: 3},
}

// DefaultWeights weight each dimension in its part's score.
var DefaultWeights = map[string]float64{
	"content-types": 1, "templates": 1.5, "titles-headings": 1, "page-shape": 1, "metadata": 1.5,
	"duplication": 1, "terminology": 1, "navigation": 1, "ownership-freshness": 0.5, "urls": 0.5,
	"click-paths": 1.5, "search": 2, "query-log": 1, "discovery-metadata": 1, "cross-linking": 1,
}

// Options tunes the audit. Zero values take the defaults in Defaults.
type Options struct {
	Parts           []string // structure, findability (default both)
	Graph           *kg.Graph
	Vocab           *vocab.Vocab
	ContentTypes    doctype.Overrides
	Templates       map[string]Template
	RequiredMeta    []string // default title, description
	RecommendedMeta []string // default tags, owner
	StaleDays       int      // default 365
	MaxDepth        int      // clicks (default 3)
	MaxNavChildren  int      // default 15
	MaxNavLevels    int      // default 3
	ShortWords      int      // default 100
	LongWords       int      // default 3000
	MaxSectionWords int      // default 600
	Weights         map[string]float64
	Tasks           []Task
	Queries         []Query
	TopK            int // a search succeeds when the page is in the top K (default 3)
	Now             time.Time
}

// Defaults fills zero values.
func (o *Options) Defaults() {
	if len(o.Parts) == 0 {
		o.Parts = []string{Structure, Findability}
	}
	if o.Templates == nil {
		o.Templates = DefaultTemplates
	}
	if o.RequiredMeta == nil {
		o.RequiredMeta = []string{"title", "description"}
	}
	if o.RecommendedMeta == nil {
		o.RecommendedMeta = []string{"tags", "owner"}
	}
	def := func(p *int, v int) {
		if *p == 0 {
			*p = v
		}
	}
	def(&o.StaleDays, 365)
	def(&o.MaxDepth, 3)
	def(&o.MaxNavChildren, 15)
	def(&o.MaxNavLevels, 3)
	def(&o.ShortWords, 100)
	def(&o.LongWords, 3000)
	def(&o.MaxSectionWords, 600)
	def(&o.TopK, 3)
	w := map[string]float64{}
	for k, v := range DefaultWeights {
		w[k] = v
	}
	for k, v := range o.Weights {
		w[k] = v
	}
	o.Weights = w
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
}

// Report is the audit result.
type Report struct {
	Source      string             `json:"source"`
	Pages       int                `json:"pages"`
	Score       float64            `json:"score"`
	PartScores  map[string]float64 `json:"part_scores"`
	Dimensions  []*Dimension       `json:"dimensions"`
	FixFirst    []Item             `json:"fix_first"`
	Tasks       []TaskResult       `json:"tasks,omitempty"`
	QueryGaps   []QueryResult      `json:"query_gaps,omitempty"`
	AgentSignal []Metric           `json:"agent_summary,omitempty"`
	Notes       []string           `json:"notes,omitempty"`
	Weights     map[string]float64 `json:"weights"`
}

// Dimension is one scored aspect.
type Dimension struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Part    string   `json:"part"`
	Score   float64  `json:"score"`  // 0-100
	Scored  bool     `json:"scored"` // false when there was nothing to score (no vocabulary, no tasks)
	Weight  float64  `json:"weight"`
	Summary string   `json:"summary"`
	Method  string   `json:"method"` // how the score is computed
	Metrics []Metric `json:"metrics,omitempty"`
	Items   []Item   `json:"items,omitempty"`
}

// Metric is a named value.
type Metric struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Item is one thing to fix.
type Item struct {
	Dimension string  `json:"dimension"`
	Page      string  `json:"page,omitempty"`
	File      string  `json:"file,omitempty"`
	Line      int     `json:"line,omitempty"`
	Message   string  `json:"message"`
	Hint      string  `json:"hint,omitempty"`
	Impact    float64 `json:"impact"`             // 0-1, how much this item matters on its own
	Priority  float64 `json:"priority,omitempty"` // impact x dimension weight x page importance
}

type auditor struct {
	s     *crawl.Site
	opt   Options
	g     *kg.Graph
	pages []*model.Page
	types map[string]string // page key -> content type
	ix    *search.Index
	r     *Report
	rank  map[string]float64
	depth map[string]int // clicks from the entry page, -1 unreachable
}

// Run audits a local or live docs set.
func Run(s *crawl.Site, opt Options) *Report {
	opt.Defaults()
	a := &auditor{s: s, opt: opt, g: opt.Graph, types: map[string]string{}, rank: map[string]float64{},
		r: &Report{Source: s.Source, PartScores: map[string]float64{}, Weights: opt.Weights}}
	if a.g == nil {
		a.g = kg.Build(s, kg.Options{Vocab: opt.Vocab, ContentTypes: opt.ContentTypes})
	}
	for _, p := range s.Pages {
		if p.Err != "" {
			continue
		}
		a.pages = append(a.pages, p)
		t, _ := doctype.Classify(p, opt.ContentTypes, crawl.Match)
		a.types[p.Key] = t
		if n := a.g.Node(kg.PageID(p.Key)); n != nil {
			a.rank[p.Key], _ = strconv.ParseFloat(n.Props["rank"], 64)
		}
	}
	sort.Slice(a.pages, func(i, j int) bool { return a.pages[i].Key < a.pages[j].Key })
	a.r.Pages = len(a.pages)
	a.depth = a.clicks(strings.TrimPrefix(a.g.Entry, "page:"))
	for _, part := range opt.Parts {
		switch part {
		case Structure:
			a.structure()
		case Findability:
			a.ix = search.Build(a.pages)
			a.findability()
		}
	}
	a.finish()
	return a.r
}

func (a *auditor) dim(id, name, part, method string) *Dimension {
	d := &Dimension{ID: id, Name: name, Part: part, Method: method, Weight: a.opt.Weights[id], Scored: true}
	a.r.Dimensions = append(a.r.Dimensions, d)
	return d
}

func (d *Dimension) item(p *model.Page, line int, impact float64, msg, hint string) {
	it := Item{Dimension: d.ID, Line: line, Message: msg, Hint: hint, Impact: impact}
	if p != nil {
		it.Page = p.Key
		it.File = fileOf(p)
	}
	d.Items = append(d.Items, it)
}

func (d *Dimension) metric(name string, format string, args ...any) {
	d.Metrics = append(d.Metrics, Metric{name, fmt.Sprintf(format, args...)})
}

// share returns 100 x ok / total, or 100 when there is nothing to count.
func share(ok, total int) float64 {
	if total == 0 {
		return 100
	}
	return round1(100 * float64(ok) / float64(total))
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }

func pct(ok, total int) string {
	if total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%d of %d (%.0f%%)", ok, total, 100*float64(ok)/float64(total))
}

func fileOf(p *model.Page) string {
	if p.RepoPath != "" {
		return p.RepoPath
	}
	if p.URL != "" {
		return p.URL
	}
	return p.Key
}

func (a *auditor) finish() {
	// part scores and the overall score are weighted means of scored dimensions
	sum, wsum := map[string]float64{}, map[string]float64{}
	var all, allW float64
	for _, d := range a.r.Dimensions {
		sort.SliceStable(d.Items, func(i, j int) bool {
			x, y := d.Items[i], d.Items[j]
			if x.Impact != y.Impact {
				return x.Impact > y.Impact
			}
			if x.File != y.File {
				return x.File < y.File
			}
			return x.Line < y.Line
		})
		if !d.Scored || d.Weight == 0 {
			continue
		}
		sum[d.Part] += d.Score * d.Weight
		wsum[d.Part] += d.Weight
		all += d.Score * d.Weight
		allW += d.Weight
	}
	for p, w := range wsum {
		a.r.PartScores[p] = round1(sum[p] / w)
	}
	if allW > 0 {
		a.r.Score = round1(all / allW)
	}
	// fix first: impact x dimension weight x page importance, one item per page and dimension
	var cands []Item
	seen := map[string]bool{}
	for _, d := range a.r.Dimensions {
		for _, it := range d.Items {
			k := d.ID + "|" + it.Page + "|" + it.Message
			if seen[k] {
				continue
			}
			seen[k] = true
			imp := 0.5
			if it.Page != "" {
				imp = 0.5 + a.rank[it.Page]/2 // rank is scaled so the top page is 1
			}
			it.Priority = math.Round(it.Impact*d.Weight*imp*1000) / 1000
			cands = append(cands, it)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Priority != cands[j].Priority {
			return cands[i].Priority > cands[j].Priority
		}
		if cands[i].File != cands[j].File {
			return cands[i].File < cands[j].File
		}
		return cands[i].Line < cands[j].Line
	})
	if len(cands) > 10 {
		cands = cands[:10]
	}
	a.r.FixFirst = cands
}

// clicks returns the click distance from start to every page, following
// content links and the navigation (parent to child).
func (a *auditor) clicks(start string) map[string]int {
	adj := map[string][]string{}
	for _, e := range a.g.Edges {
		if e.Broken {
			continue
		}
		from, to := pageKey(e.From), pageKey(e.To)
		if from == "" || to == "" || from == to {
			continue
		}
		switch {
		case e.Type == kg.ELinksTo:
			adj[from] = append(adj[from], to)
		case e.Type == kg.EChildOf && strings.HasPrefix(e.Span, "navigation:"):
			adj[to] = append(adj[to], from) // the parent's menu lists the child
		}
	}
	d := map[string]int{}
	for _, p := range a.pages {
		d[p.Key] = -1
	}
	if _, ok := d[start]; !ok {
		return d
	}
	// a navigation menu is on every page: top-level entries are one click away
	d[start] = 0
	queue := []string{start}
	if nav := a.s.Nav; nav != nil {
		for _, e := range nav.Entries {
			a.topLevel(e, func(k string) {
				if v, ok := d[k]; ok && v == -1 {
					d[k] = 1
					queue = append(queue, k)
				}
			})
		}
	}
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		for _, y := range adj[x] {
			if v, ok := d[y]; ok && v == -1 {
				d[y] = d[x] + 1
				queue = append(queue, y)
			}
		}
	}
	return d
}

// topLevel calls f for the first page reachable in each top-level menu
// entry: a category without a page exposes its children.
func (a *auditor) topLevel(e *crawl.NavEntry, f func(string)) {
	if e.Key != "" {
		f(e.Key)
		return
	}
	for _, c := range e.Children {
		a.topLevel(c, f)
	}
}

func pageKey(id string) string {
	switch {
	case strings.HasPrefix(id, "page:"):
		return strings.TrimPrefix(id, "page:")
	case strings.HasPrefix(id, "section:"):
		k := strings.TrimPrefix(id, "section:")
		if i := strings.LastIndex(k, "#"); i >= 0 {
			k = k[:i]
		}
		return k
	}
	return ""
}
