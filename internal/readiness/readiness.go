// Package readiness scores how well AI agents can discover, navigate and
// consume a docs set. It combines afdocs (run as a subprocess) with checks
// that need the whole knowledge graph, which afdocs' page sampling can't see.
package readiness

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/graph"
	"github.com/kakabisht/Docs-toolkit/internal/links"
)

// Status of a check.
const (
	Pass = "pass"
	Warn = "warn"
	Fail = "fail"
	Skip = "skip"
)

// Check is one graph-level readiness check.
type Check struct {
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	Score    float64  `json:"score"` // 0..1
	Weight   float64  `json:"weight"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// Options tunes the graph checks.
type Options struct {
	MaxPageChars int
	MaxDepth     int
}

// Fix is a prioritized recommendation.
type Fix struct {
	Source   string  `json:"source"` // docsbot | afdocs
	Check    string  `json:"check"`
	Status   string  `json:"status"`
	Priority float64 `json:"priority"`
	Text     string  `json:"text"`
}

// Report is the combined agent readiness result.
type Report struct {
	Source      string        `json:"source"`
	Score       int           `json:"score"`
	Grade       string        `json:"grade"`
	GraphScore  int           `json:"graph_score"`
	AfdocsScore *int          `json:"afdocs_score,omitempty"`
	AfdocsGrade string        `json:"afdocs_grade,omitempty"`
	AfdocsNote  string        `json:"afdocs_note,omitempty"`
	Capped      *Capped       `json:"capped,omitempty"` // afdocs capped its score: the headline shows the blocker, not the capped number
	Mode        string        `json:"mode"`             // audit or indicative
	Sample      *Sample       `json:"sample,omitempty"`
	Warnings    []string      `json:"warnings,omitempty"`
	Checks      []Check       `json:"checks"`
	Afdocs      *AfdocsResult `json:"afdocs,omitempty"`
	Fixes       []Fix         `json:"fixes"`
}

var vagueText = map[string]bool{"here": true, "click here": true, "this": true, "link": true, "read more": true, "more": true, "this page": true, "learn more": true, "see here": true, "this link": true, "docs": true}

// GraphChecks runs the checks that need the whole graph. lr is an internal-only
// link report (may be nil to skip the link check).
func GraphChecks(g *graph.Graph, lr *links.Report, opt Options) []Check {
	if opt.MaxPageChars <= 0 {
		opt.MaxPageChars = 50000
	}
	if opt.MaxDepth <= 0 {
		opt.MaxDepth = 4
	}
	var pages []*graph.Node
	for _, n := range g.Nodes {
		if n.Err == "" && !n.Draft {
			pages = append(pages, n)
		}
	}
	total := float64(len(pages))
	var out []Check
	ratio := func(bad int) float64 {
		if total == 0 {
			return 1
		}
		return 1 - float64(bad)/total
	}

	// llms.txt presence
	{
		c := Check{ID: "llms-txt", Title: "llms.txt exists", Weight: 3}
		if g.LLMSTxt.Found {
			c.Status, c.Score = Pass, 1
			c.Message = fmt.Sprintf("found at %s (%d links, %s)", g.LLMSTxt.Location, len(g.LLMSTxt.Links), humanBytes(g.LLMSTxt.Size))
		} else {
			c.Status, c.Score = Fail, 0
			c.Message = "no llms.txt found (checked " + g.LLMSTxt.Location + ")"
			c.Fix = "Publish /llms.txt: a markdown index with one link per page and a one-line description. Generate it from the graph with `docsbot graph export --as llms-txt`."
		}
		out = append(out, c)
	}

	// llms.txt coverage
	{
		c := Check{ID: "llms-txt-coverage", Title: "llms.txt lists every page", Weight: 2}
		if !g.LLMSTxt.Found {
			c.Status, c.Message = Skip, "no llms.txt"
		} else {
			var missing []string
			for _, n := range pages {
				if !n.InLLMSTxt {
					missing = append(missing, n.Key)
				}
			}
			c.Score = ratio(len(missing))
			c.Status = grade3(c.Score, 0.95, 0.8)
			c.Message = fmt.Sprintf("%d of %d pages listed (%.0f%%)", len(pages)-len(missing), len(pages), c.Score*100)
			if len(missing) > 0 {
				c.Fix = "Add the missing pages to llms.txt so agents can find them without crawling."
				c.Examples = sample(missing, 5)
			}
		}
		out = append(out, c)
	}

	// orphans
	{
		c := Check{ID: "orphan-pages", Title: "No orphan pages", Weight: 2} // nothing links to the page, not even navigation
		var orphans []string
		for _, n := range pages {
			if n.In == 0 && n.Key != g.Entry {
				orphans = append(orphans, n.Key)
			}
		}
		c.Score = ratio(len(orphans))
		c.Status = grade3(c.Score, 0.98, 0.9)
		c.Message = fmt.Sprintf("%s nothing links to, not even navigation", pages_(len(orphans)))
		if len(orphans) > 0 {
			c.Fix = "Link orphan pages from a parent or related page. Agents that navigate by following links will never reach them."
			c.Examples = sample(orphans, 5)
		}
		out = append(out, c)
	}

	// click depth
	{
		c := Check{ID: "click-depth", Title: fmt.Sprintf("Pages within %d clicks of the entry page", opt.MaxDepth), Weight: 1.5}
		var deep []string
		for _, n := range pages {
			if n.Depth < 0 || n.Depth > opt.MaxDepth {
				deep = append(deep, n.Key)
			}
		}
		c.Score = ratio(len(deep))
		c.Status = grade3(c.Score, 0.95, 0.8)
		c.Message = fmt.Sprintf("%s deeper than %d clicks or unreachable from %s", pages_(len(deep)), opt.MaxDepth, g.Entry)
		if len(deep) > 0 {
			c.Fix = "Flatten navigation: link deep pages from section index pages so each is a few hops from the entry."
			c.Examples = sample(deep, 5)
		}
		out = append(out, c)
	}

	// internal links
	if lr != nil {
		c := Check{ID: "internal-links", Title: "Internal links and anchors resolve", Weight: 3}
		bad := map[string]bool{}
		occ := 0
		var ex []string
		for _, is := range lr.Issues {
			if (is.Problem == links.Broken || is.Problem == links.BrokenAnchor) && is.Kind != "external" {
				occ += len(is.Sources)
				for _, s := range is.Sources {
					bad[s.Page] = true
				}
				ex = append(ex, is.Target)
			}
		}
		c.Score = ratio(len(bad))
		c.Status = grade3(c.Score, 1, 0.95)
		c.Message = fmt.Sprintf("%d broken internal links or anchors on %s", occ, pages_(len(bad)))
		if occ > 0 {
			c.Fix = "Fix broken internal links (`docsbot links` lists them by impact). Agents treat a 404 as a dead end."
			c.Examples = sample(ex, 5)
		}
		out = append(out, c)
	}

	// titles
	{
		c := Check{ID: "page-titles", Title: "Every page has a title", Weight: 1}
		var bad []string
		for _, n := range pages {
			if strings.TrimSpace(n.Title) == "" {
				bad = append(bad, n.Key)
			}
		}
		c.Score = ratio(len(bad))
		c.Status = grade3(c.Score, 1, 0.95)
		c.Message = fmt.Sprintf("%s without a title or H1", pages_(len(bad)))
		if len(bad) > 0 {
			c.Fix = "Give each page a title (front matter `title:` or a single H1)."
			c.Examples = sample(bad, 5)
		}
		out = append(out, c)
	}

	// descriptions
	{
		c := Check{ID: "page-descriptions", Title: "Pages have a description", Weight: 1}
		var bad []string
		for _, n := range pages {
			if strings.TrimSpace(n.Description) == "" {
				bad = append(bad, n.Key)
			}
		}
		c.Score = ratio(len(bad))
		c.Status = grade3(c.Score, 0.9, 0.6)
		c.Message = fmt.Sprintf("%d of %d pages have a description", len(pages)-len(bad), len(pages))
		if len(bad) > 0 {
			c.Fix = "Add a one-sentence description (front matter or <meta name=description>). It becomes the llms.txt summary agents use to pick pages."
			c.Examples = sample(bad, 5)
		}
		out = append(out, c)
	}

	// heading structure
	{
		c := Check{ID: "heading-structure", Title: "Headings don't skip levels", Weight: 1}
		var bad []string
		for _, n := range pages {
			prev := 1 // the page title stands in for the H1
			h1 := 0
			for _, h := range n.Sections {
				if h.Level == 1 {
					h1++
				}
				if h.Level > prev+1 {
					bad = append(bad, fmt.Sprintf("%s (H%d → H%d at %q)", n.Key, prev, h.Level, h.Text))
					break
				}
				prev = h.Level
			}
			if h1 > 1 {
				bad = append(bad, fmt.Sprintf("%s (%d H1s)", n.Key, h1))
			}
		}
		c.Score = ratio(len(bad))
		c.Status = grade3(c.Score, 0.95, 0.85)
		c.Message = fmt.Sprintf("%s with skipped heading levels or several H1s", pages_(len(bad)))
		if len(bad) > 0 {
			c.Fix = "Keep a clean H1 > H2 > H3 outline. Agents chunk pages by heading, and gaps produce orphaned chunks."
			c.Examples = sample(bad, 5)
		}
		out = append(out, c)
	}

	// page size
	{
		c := Check{ID: "page-size", Title: fmt.Sprintf("Pages under %s characters", humanInt(opt.MaxPageChars)), Weight: 1.5}
		var bad []string
		for _, n := range pages {
			if n.Chars > opt.MaxPageChars {
				bad = append(bad, fmt.Sprintf("%s (%s chars)", n.Key, humanInt(n.Chars)))
			}
		}
		c.Score = ratio(len(bad))
		c.Status = grade3(c.Score, 0.98, 0.9)
		c.Message = fmt.Sprintf("%s large enough to be truncated by agent context windows", pages_(len(bad)))
		if len(bad) > 0 {
			c.Fix = "Split very long pages into focused subpages, or offer a markdown version without navigation chrome."
			c.Examples = sample(bad, 5)
		}
		out = append(out, c)
	}

	// code block languages
	{
		c := Check{ID: "code-languages", Title: "Code blocks declare a language", Weight: 1}
		tot, bad := 0, 0
		var ex []string
		for _, n := range pages {
			for _, cb := range n.Code {
				tot++
				if cb.Lang == "" {
					bad++
					if len(ex) < 5 {
						loc := n.Key
						if n.Path != "" && cb.Line > 0 {
							loc = fmt.Sprintf("%s:%d", n.Path, cb.Line)
						}
						ex = append(ex, loc)
					}
				}
			}
		}
		if tot == 0 {
			c.Status, c.Score, c.Message = Skip, 1, "no code blocks"
		} else {
			c.Score = 1 - float64(bad)/float64(tot)
			c.Status = grade3(c.Score, 0.95, 0.8)
			c.Message = fmt.Sprintf("%d of %d code blocks have no language tag", bad, tot)
			if bad > 0 {
				c.Fix = "Tag fenced code blocks (```bash, ```go). Agents use the tag to tell commands from output and config."
				c.Examples = ex
			}
		}
		out = append(out, c)
	}

	// link text
	{
		c := Check{ID: "link-text", Title: "Links have descriptive text", Weight: 0.5}
		tot, bad := 0, 0
		var ex []string
		for _, e := range g.Edges {
			if e.Image || e.Nav || (e.Kind != "internal" && e.Kind != "external") {
				continue
			}
			tot++
			t := strings.ToLower(strings.Trim(strings.TrimSpace(e.Text), ".:!"))
			if vagueText[t] {
				bad++
				if len(ex) < 5 {
					ex = append(ex, fmt.Sprintf("%s: %q → %s", e.From, e.Text, e.Href))
				}
			}
		}
		if tot == 0 {
			c.Status, c.Score, c.Message = Skip, 1, "no links"
		} else {
			c.Score = 1 - float64(bad)/float64(tot)
			c.Status = grade3(c.Score, 0.99, 0.95)
			c.Message = fmt.Sprintf("%d of %d links use vague text like \"here\"", bad, tot)
			if bad > 0 {
				c.Fix = "Describe the destination in the link text. Agents decide which link to follow from its text."
				c.Examples = ex
			}
		}
		out = append(out, c)
	}
	return out
}

// GraphScore is the weighted score (0-100) of non-skipped checks.
func GraphScore(cs []Check) int {
	var w, s float64
	for _, c := range cs {
		if c.Status == Skip {
			continue
		}
		w += c.Weight
		s += c.Weight * c.Score
	}
	if w == 0 {
		return 0
	}
	return int(math.Round(100 * s / w))
}

// Capped records an afdocs score cap. afdocs caps its overall score when a
// critical check fails (no llms.txt, most pages rendered client-side, …), so
// the capped number says "blocked" rather than "how good". docsbot reports the
// blocker and the score before the cap instead of headlining the capped value.
type Capped struct {
	Cap     int    `json:"cap"`
	Raw     int    `json:"raw"`
	CheckID string `json:"check_id"`
	Reason  string `json:"reason"`
}

// Combine builds the final report.
func Combine(source string, checks []Check, af *AfdocsResult, afNote string, afWeight float64) *Report {
	r := &Report{Source: source, Checks: checks, GraphScore: GraphScore(checks), Afdocs: af, AfdocsNote: afNote}
	r.Score = r.GraphScore
	if af != nil && af.Scoring != nil {
		raw := af.Scoring.RawScore()
		v := int(math.Round(raw))
		r.AfdocsScore = &v
		r.AfdocsGrade = Grade(v)
		if c := af.Scoring.Cap; c != nil {
			r.Capped = &Capped{Cap: int(c.Cap), Raw: v, CheckID: c.CheckID, Reason: c.Reason}
		}
		r.Score = int(math.Round(afWeight*raw + (1-afWeight)*float64(r.GraphScore)))
	}
	r.Grade = Grade(r.Score)
	for _, c := range checks {
		if (c.Status == Warn || c.Status == Fail) && c.Fix != "" {
			r.Fixes = append(r.Fixes, Fix{Source: "docsbot", Check: c.ID, Status: c.Status, Priority: c.Weight * (1 - c.Score) * 10, Text: c.Fix})
		}
	}
	if af != nil {
		for _, res := range af.Results {
			if res.Status != "warn" && res.Status != "fail" {
				continue
			}
			text := res.Message
			if af.Scoring != nil {
				if fix, ok := af.Scoring.Resolutions[res.ID]; ok && fix != "" {
					text = fix
				}
			}
			p := 1.0
			if af.Scoring != nil {
				if cs, ok := af.Scoring.CheckScores[res.ID]; ok {
					p = cs.MaxScore - cs.EarnedScore
				}
			}
			if res.Status == "fail" {
				p += 0.5
			}
			r.Fixes = append(r.Fixes, Fix{Source: "afdocs", Check: res.ID, Status: res.Status, Priority: p, Text: text})
		}
	}
	sort.SliceStable(r.Fixes, func(i, j int) bool { return r.Fixes[i].Priority > r.Fixes[j].Priority })
	return r
}

// Grade maps a 0-100 score to a letter.
func Grade(s int) string {
	switch {
	case s >= 90:
		return "A"
	case s >= 80:
		return "B"
	case s >= 70:
		return "C"
	case s >= 60:
		return "D"
	}
	return "F"
}

func grade3(score, pass, warn float64) string {
	switch {
	case score >= pass:
		return Pass
	case score >= warn:
		return Warn
	}
	return Fail
}

func sample(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return append(append([]string(nil), s[:n]...), fmt.Sprintf("… and %d more", len(s)-n))
}

func humanBytes(n int) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	return fmt.Sprintf("%.1f KB", float64(n)/1024)
}

func humanInt(n int) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func pages_(n int) string {
	if n == 1 {
		return "1 page"
	}
	return fmt.Sprintf("%d pages", n)
}
