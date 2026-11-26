package audit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/docsbot/docsbot/internal/kg"
	"github.com/docsbot/docsbot/internal/model"
	"github.com/docsbot/docsbot/internal/search"
)

// TaskResult is how findable one task's answer is.
type TaskResult struct {
	Task    string        `json:"task"`
	Expect  []string      `json:"expect"`
	Pages   []string      `json:"pages"` // expected pages that exist
	Queries []QueryResult `json:"queries"`
	Clicks  int           `json:"clicks"` // fewest clicks from the start page, -1 unreachable
	Found   bool          `json:"found"`  // every query ranks an expected page in the top K
	Problem string        `json:"problem,omitempty"`
}

// QueryResult is one search.
type QueryResult struct {
	Query string   `json:"query"`
	Count int      `json:"count,omitempty"`
	Rank  int      `json:"rank"` // best rank of an expected page, 0 when not found
	Top   []string `json:"top"`  // the top results
}

func (a *auditor) findability() {
	a.clickPaths()
	a.searchTasks()
	a.queryLog()
	a.discovery()
	a.crossLinking()
	a.agentSummary()
}

func (a *auditor) clickPaths() {
	d := a.dim("click-paths", "Click paths", Findability,
		fmt.Sprintf("Share of pages reachable within %d clicks of the entry page, following content links and the navigation menu.", a.opt.MaxDepth))
	within, unreachable := 0, 0
	hist := map[int]int{}
	for _, p := range a.pages {
		c := a.depth[p.Key]
		switch {
		case c < 0:
			unreachable++
			if !p.Draft {
				d.item(p, 0, 0.6, "Can't be reached by clicking from the entry page", "Link to it from a related page or add it to the navigation.")
			}
		case c <= a.opt.MaxDepth:
			within++
			hist[c]++
		default:
			hist[c]++
			d.item(p, 0, 0.2+0.1*float64(min(c-a.opt.MaxDepth, 3)), fmt.Sprintf("%d clicks from the entry page", c), "Link to it from a page nearer the top.")
		}
	}
	d.Score = share(within, len(a.pages))
	d.Summary = fmt.Sprintf("%s pages within %d clicks; %d unreachable", pct(within, len(a.pages)), a.opt.MaxDepth, unreachable)
	var ks []int
	for k := range hist {
		ks = append(ks, k)
	}
	sort.Ints(ks)
	var parts []string
	for _, k := range ks {
		parts = append(parts, fmt.Sprintf("%d: %d", k, hist[k]))
	}
	d.metric("pages by clicks", "%s", strings.Join(parts, ", "))
	d.metric("entry page", "%s", strings.TrimPrefix(a.g.Entry, "page:"))
}

func (a *auditor) top(q string) []string {
	var out []string
	for _, h := range a.ix.Search(q, a.opt.TopK) {
		out = append(out, h.Key)
	}
	return out
}

func (a *auditor) searchTasks() {
	if len(a.opt.Tasks) == 0 {
		d := a.dim("search", "Search (title proxy)", Findability,
			fmt.Sprintf("No tasks file, so this is a proxy: share of pages that rank in the top %d of a local BM25 search for their own title. Low scores mean titles that don't distinguish pages. Give tasks with --tasks for a real test.", a.opt.TopK))
		ok, n := 0, 0
		for _, p := range a.pages {
			if p.Title == "" || p.Draft {
				continue
			}
			n++
			if r := a.ix.Rank(p.Title, p.Key, a.opt.TopK); r > 0 {
				ok++
				continue
			}
			d.item(p, 1, 0.3, fmt.Sprintf("A search for its own title %q doesn't rank it in the top %d (top: %s)", p.Title, a.opt.TopK, strings.Join(a.top(p.Title), ", ")), "Make the title more specific, or differentiate the pages that outrank it.")
		}
		d.Score = share(ok, n)
		d.Summary = fmt.Sprintf("%s titles find their own page (proxy; no tasks file)", pct(ok, n))
		return
	}
	d := a.dim("search", "Search tasks", Findability,
		fmt.Sprintf("Share of task queries for which a local BM25 search ranks an expected page in the top %d. A local index stands in for the site's search.", a.opt.TopK))
	total, ok := 0, 0
	for _, t := range a.opt.Tasks {
		tr := TaskResult{Task: t.Task, Expect: t.Expect, Clicks: -1, Found: true}
		want := map[string]bool{}
		for _, e := range t.Expect {
			if p := a.s.Find(e); p != nil {
				want[p.Key] = true
				tr.Pages = append(tr.Pages, p.Key)
			}
		}
		if len(want) == 0 {
			tr.Problem = "none of the expected pages exist: " + strings.Join(t.Expect, ", ")
			tr.Found = false
			d.item(nil, 0, 0.8, fmt.Sprintf("Task %q: %s", t.Task, tr.Problem), "Fix the tasks file, or write the page.")
			a.r.Tasks = append(a.r.Tasks, tr)
			total += len(t.Queries)
			continue
		}
		// browsing: fewest clicks from the start page to any answer
		depth := a.depth
		if t.Start != "" {
			if sp := a.s.Find(t.Start); sp != nil {
				depth = a.clicks(sp.Key)
			}
		}
		for k := range want {
			if c := depth[k]; c >= 0 && (tr.Clicks < 0 || c < tr.Clicks) {
				tr.Clicks = c
			}
		}
		for _, q := range t.Queries {
			total++
			hits := a.ix.Search(q, 10)
			qr := QueryResult{Query: q}
			for i, h := range hits {
				if i < a.opt.TopK {
					qr.Top = append(qr.Top, h.Key)
				}
				if want[h.Key] && qr.Rank == 0 {
					qr.Rank = i + 1
				}
			}
			if qr.Rank > 0 && qr.Rank <= a.opt.TopK {
				ok++
			} else {
				tr.Found = false
				where := "not in the top 10"
				if qr.Rank > 0 {
					where = fmt.Sprintf("ranks %d", qr.Rank)
				}
				p := a.s.Page(tr.Pages[0])
				d.item(p, 1, 0.7, fmt.Sprintf("Task %q: search %q %s; top results are %s", t.Task, q, where, orNone(qr.Top)),
					"Use the reader's words in the title, headings or description of the answer page.")
			}
			tr.Queries = append(tr.Queries, qr)
		}
		a.r.Tasks = append(a.r.Tasks, tr)
	}
	d.Score = share(ok, total)
	found := 0
	for _, t := range a.r.Tasks {
		if t.Found {
			found++
		}
	}
	d.Summary = fmt.Sprintf("%s queries find an answer in the top %d; %s tasks pass every query", pct(ok, total), a.opt.TopK, pct(found, len(a.r.Tasks)))
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "nothing"
	}
	return strings.Join(s, ", ")
}

func (a *auditor) queryLog() {
	if len(a.opt.Queries) == 0 {
		return
	}
	d := a.dim("query-log", "Query log coverage", Findability,
		"Share of logged searches (weighted by count) for which a local BM25 search matches every query term on at least one page. Unmatched searches are likely content or vocabulary gaps.")
	total, ok := 0, 0
	var gaps []QueryResult
	for _, q := range a.opt.Queries {
		total += q.Count
		hits := a.ix.Search(q.Query, a.opt.TopK)
		if len(hits) > 0 && a.coversAll(q.Query, hits[0].Key) {
			ok += q.Count
			continue
		}
		qr := QueryResult{Query: q.Query, Count: q.Count}
		for _, h := range hits {
			qr.Top = append(qr.Top, h.Key)
		}
		gaps = append(gaps, qr)
	}
	sort.SliceStable(gaps, func(i, j int) bool {
		if gaps[i].Count != gaps[j].Count {
			return gaps[i].Count > gaps[j].Count
		}
		return gaps[i].Query < gaps[j].Query
	})
	for _, g := range gaps {
		imp := 0.3 + 0.5*float64(g.Count)/float64(max(total, 1))
		msg := fmt.Sprintf("Search %q (%d times) has no page that matches all its words", g.Query, g.Count)
		if len(g.Top) > 0 {
			msg += "; nearest: " + strings.Join(g.Top, ", ")
		}
		d.item(nil, 0, min(imp, 1), msg, "Add the content, or use the reader's words on the page that answers it.")
	}
	a.r.QueryGaps = gaps
	if len(a.r.QueryGaps) > 50 {
		a.r.QueryGaps = a.r.QueryGaps[:50]
	}
	d.Score = share(ok, total)
	d.Summary = fmt.Sprintf("%d distinct searches, %s of all searches matched; %d gaps", len(a.opt.Queries), pct(ok, total), len(gaps))
}

// coversAll reports whether every term of q appears on page key.
func (a *auditor) coversAll(q, key string) bool {
	p := a.s.Page(key)
	if p == nil {
		return false
	}
	var sb strings.Builder
	sb.WriteString(p.Title + " " + p.Description + " " + strings.Join(p.Tags, " "))
	for _, h := range p.Headings {
		sb.WriteString(" " + h.Text)
	}
	for _, para := range p.Paragraphs {
		sb.WriteString(" " + para.Text)
	}
	for _, c := range p.CodeBlocks {
		sb.WriteString(" " + c.Content)
	}
	for _, spans := range [][]model.Span{p.InlineCode, p.DefTerms, p.TableCells} {
		for _, c := range spans {
			sb.WriteString(" " + c.Text)
		}
	}
	have := map[string]bool{}
	for _, t := range search.Tokens(sb.String()) {
		have[t] = true
	}
	for _, t := range search.Tokens(q) {
		if !have[t] {
			return false
		}
	}
	return true
}

func (a *auditor) discovery() {
	d := a.dim("discovery-metadata", "Metadata for discovery", Findability,
		"Mean per page of three checks: a description of 50 to 160 characters, a specific title (not generic, not shared), and tags when the site uses tags.")
	usesTags := false
	titles := map[string]int{}
	for _, p := range a.pages {
		usesTags = usesTags || len(p.Tags) > 0
		titles[strings.ToLower(p.Title)]++
	}
	var total float64
	for _, p := range a.pages {
		checks, pass := 2, 0
		n := len([]rune(strings.TrimSpace(p.Description)))
		switch {
		case n == 0:
			d.item(p, 1, 0.4, "No description: search engines and llms.txt fall back to the first lines", "Write one or two sentences on what the page helps the reader do.")
		case n < 50:
			d.item(p, 1, 0.2, fmt.Sprintf("Description is %d characters; aim for 50 to 160", n), "")
		case n > 160:
			d.item(p, 1, 0.15, fmt.Sprintf("Description is %d characters; results cut it at about 160", n), "")
		default:
			pass++
		}
		t := strings.ToLower(strings.TrimSpace(p.Title))
		if t != "" && titles[t] == 1 && !genericHeading[t] {
			pass++
		}
		if usesTags {
			checks++
			if len(p.Tags) > 0 {
				pass++
			}
		}
		total += float64(pass) / float64(checks)
	}
	if len(a.pages) > 0 {
		d.Score = round1(100 * total / float64(len(a.pages)))
	}
	d.Summary = fmt.Sprintf("titles, descriptions%s checked on %d pages", map[bool]string{true: " and tags", false: ""}[usesTags], len(a.pages))
}

func (a *auditor) crossLinking() {
	d := a.dim("cross-linking", "Cross-linking", Findability,
		"Share of pages with at least two content links from other pages (the entry page and landing pages excepted). Similar pages that don't link to each other are listed as suggestions; similarity is inferred.")
	in := map[string]map[string]bool{}
	linked := map[[2]string]bool{}
	for _, e := range a.g.Edges {
		if e.Type != kg.ELinksTo || e.Broken {
			continue
		}
		f, t := pageKey(e.From), pageKey(e.To)
		if f == "" || t == "" || f == t {
			continue
		}
		if in[t] == nil {
			in[t] = map[string]bool{}
		}
		in[t][f] = true
		linked[[2]string{f, t}], linked[[2]string{t, f}] = true, true
	}
	ok, n := 0, 0
	entry := strings.TrimPrefix(a.g.Entry, "page:")
	for _, p := range a.pages {
		if p.Key == entry || a.isLanding(p) || p.Draft {
			continue
		}
		n++
		if len(in[p.Key]) >= 2 {
			ok++
			continue
		}
		d.item(p, 0, 0.25, fmt.Sprintf("Only %d other page%s here in the content", len(in[p.Key]), map[bool]string{true: " links", false: "s link"}[len(in[p.Key]) == 1]), "Link to it where readers need it.")
	}
	sugg := 0
	for _, e := range a.g.Edges {
		if e.Type != kg.ERelated {
			continue
		}
		f, t := pageKey(e.From), pageKey(e.To)
		if linked[[2]string{f, t}] {
			continue
		}
		sugg++
		if p := a.s.Page(f); p != nil {
			d.item(p, 0, 0.1+0.2*e.Confidence, fmt.Sprintf("Suggestion: link to %s (similar content, %s)", t, e.Span), "Inferred from text similarity: check that the link helps the reader.")
		}
	}
	d.Score = share(ok, n)
	d.Summary = fmt.Sprintf("%s pages have two or more inbound content links; %d similar pairs don't link", pct(ok, n), sugg)
}

func (a *auditor) agentSummary() {
	var withDesc, long, code, codeLang int
	for _, p := range a.pages {
		if p.Description != "" {
			withDesc++
		}
		if p.Chars > 50000 {
			long++
		}
		for _, c := range p.CodeBlocks {
			code++
			if c.Lang != "" {
				codeLang++
			}
		}
	}
	m := []Metric{
		{"llms.txt", map[bool]string{true: "found at " + a.s.LLMSTxt.Location, false: "not found"}[a.s.LLMSTxt.Found]},
		{"pages with a description (used for llms.txt entries)", pct(withDesc, len(a.pages))},
		{"pages over 50,000 characters", fmt.Sprint(long)},
		{"code blocks with a language", pct(codeLang, code)},
	}
	a.r.AgentSignal = m
	a.r.Notes = append(a.r.Notes, "The agent summary is a pointer, not a score: run `docsbot agent-ready check` for the full agent-readiness checks.")
}
