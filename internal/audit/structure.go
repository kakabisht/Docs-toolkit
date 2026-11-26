package audit

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/kg"
	"github.com/kakabisht/Docs-toolkit/internal/model"
)

func (a *auditor) structure() {
	a.contentTypes()
	a.templates()
	a.titles()
	a.shape()
	a.metadata()
	a.duplication()
	a.terminology()
	a.navigation()
	a.freshness()
	a.urls()
}

func (a *auditor) isLanding(p *model.Page) bool {
	return a.types[p.Key] == "landing" || p.Key == "/" || crawl.IsIndexFile(p.Path)
}

func (a *auditor) contentTypes() {
	d := a.dim("content-types", "Content types", Structure,
		"Share of pages whose content type is declared (front matter or config). Inferred types are a guess from path, title and shape.")
	dist := map[string]int{}
	declared := 0
	for _, p := range a.pages {
		t := a.types[p.Key]
		dist[t]++
		if p.Type != "" || a.overridden(p) {
			declared++
			continue
		}
		d.item(p, 1, 0.2, fmt.Sprintf("Content type not declared; it looks like %s", t), "Add content_type (or type) to the front matter, or a glob in graph.content_types.")
	}
	d.Score = share(declared, len(a.pages))
	d.Summary = fmt.Sprintf("%s pages declare a content type", pct(declared, len(a.pages)))
	keys := make([]string, 0, len(dist))
	for k := range dist {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if dist[keys[i]] != dist[keys[j]] {
			return dist[keys[i]] > dist[keys[j]]
		}
		return keys[i] < keys[j]
	})
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, dist[k]))
	}
	d.metric("distribution", "%s", strings.Join(parts, ", "))
}

func (a *auditor) overridden(p *model.Page) bool {
	rel := strings.TrimPrefix(p.Key, "/")
	for glob := range a.opt.ContentTypes {
		if crawl.Match(glob, rel) || (p.Path != "" && crawl.Match(glob, p.Path)) {
			return true
		}
	}
	return false
}

var stepHeading = regexp.MustCompile(`(?i)^(step\s*\d+|\d+[.)]\s)`)

func (a *auditor) templates() {
	d := a.dim("templates", "Template conformance", Structure,
		"Share of pages that meet the template for their content type (steps for how-to and tutorials, a table, definition list or code for reference, links for landing pages, required headings).")
	checked, ok := 0, 0
	for _, p := range a.pages {
		t, has := a.opt.Templates[a.types[p.Key]]
		if !has {
			continue
		}
		checked++
		var missing []string
		if t.Steps && !hasSteps(p) {
			missing = append(missing, "numbered steps")
		}
		if t.Structured && p.Tables == 0 && len(p.DefTerms) == 0 && len(p.CodeBlocks) == 0 {
			missing = append(missing, "a table, definition list or code example")
		}
		if t.MinLinks > 0 && internalLinks(p) < t.MinLinks {
			missing = append(missing, fmt.Sprintf("at least %d links to other pages", t.MinLinks))
		}
		for _, re := range t.RequiredHeadings {
			rx, err := regexp.Compile("(?i)" + re)
			if err != nil {
				continue
			}
			found := false
			for _, h := range p.Headings {
				found = found || rx.MatchString(h.Text)
			}
			if !found {
				missing = append(missing, fmt.Sprintf("a heading matching %q", re))
			}
		}
		if len(missing) == 0 {
			ok++
			continue
		}
		d.item(p, 1, 0.5, fmt.Sprintf("%s page lacks %s", a.types[p.Key], strings.Join(missing, " and ")), "Restructure the page to the template, or correct its content type.")
	}
	d.Score = share(ok, checked)
	d.Summary = fmt.Sprintf("%s pages with a template conform", pct(ok, checked))
	var names []string
	for k := range a.opt.Templates {
		names = append(names, k)
	}
	sort.Strings(names)
	d.metric("templates", "%s", strings.Join(names, ", "))
}

func hasSteps(p *model.Page) bool {
	for _, l := range p.Lists {
		if l.Ordered && l.Items >= 2 {
			return true
		}
	}
	n := 0
	for _, h := range p.Headings {
		if stepHeading.MatchString(h.Text) {
			n++
		}
	}
	return n >= 2
}

func internalLinks(p *model.Page) int {
	n := 0
	for _, l := range p.Links {
		if !l.Image && !l.Nav && !strings.HasPrefix(l.Href, "http") && !strings.HasPrefix(l.Href, "#") && !strings.HasPrefix(l.Href, "mailto:") {
			n++
		}
	}
	return n
}

var genericHeading = map[string]bool{"overview": true, "introduction": true, "details": true, "more": true, "other": true,
	"general": true, "misc": true, "miscellaneous": true, "notes": true, "info": true, "information": true, "about": true, "summary": true}

var minorWords = map[string]bool{"a": true, "an": true, "the": true, "and": true, "or": true, "but": true, "for": true, "nor": true,
	"of": true, "on": true, "in": true, "to": true, "at": true, "by": true, "with": true, "from": true, "as": true, "vs": true, "via": true, "into": true}

// headingCase is "title" when every major word is capitalised, "sentence"
// when only the first is, and "" when it can't tell (short, or code).
func headingCase(t string) string {
	words := strings.Fields(t)
	if len(words) < 3 || strings.ContainsAny(t, "`") {
		return ""
	}
	capped, major := 0, 0
	for i, w := range words[1:] {
		_ = i
		r := []rune(strings.Trim(w, `"'(),:;!?`))
		if len(r) == 0 || minorWords[strings.ToLower(string(r))] || !unicode.IsLetter(r[0]) {
			continue
		}
		// acronyms and identifiers say nothing about style
		if strings.ToUpper(string(r)) == string(r) || strings.ContainsAny(string(r[1:]), "ABCDEFGHIJKLMNOPQRSTUVWXYZ._-") {
			continue
		}
		major++
		if unicode.IsUpper(r[0]) {
			capped++
		}
	}
	switch {
	case major < 2:
		return ""
	case capped == major:
		return "title"
	case capped == 0:
		return "sentence"
	}
	return ""
}

func (a *auditor) titles() {
	d := a.dim("titles-headings", "Titles and headings", Structure,
		"Share of pages with no title or heading problem: missing or long titles (over 70 characters), duplicate titles, skipped heading levels, generic headings, and headings in the minority case style.")
	bad := map[string]bool{}
	titles := map[string][]*model.Page{}
	styles := map[string]int{}
	for _, p := range a.pages {
		for _, h := range p.Headings {
			if s := headingCase(h.Text); s != "" {
				styles[s]++
			}
		}
	}
	major := "sentence"
	if styles["title"] > styles["sentence"] {
		major = "title"
	}
	for _, p := range a.pages {
		if p.Title == "" {
			d.item(p, 1, 0.8, "Page has no title", "Add a title in front matter or a level-1 heading.")
			bad[p.Key] = true
		} else {
			titles[strings.ToLower(p.Title)] = append(titles[strings.ToLower(p.Title)], p)
			if n := len([]rune(p.Title)); n > 70 {
				d.item(p, 1, 0.3, fmt.Sprintf("Title is %d characters; search results and tabs cut it at about 60-70", n), "Shorten the title; move detail to the description.")
				bad[p.Key] = true
			}
		}
		prev := 1
		for _, h := range p.Headings {
			if h.Level > prev+1 && prev > 0 {
				d.item(p, h.Line, 0.2, fmt.Sprintf("Heading level jumps from %d to %d: %q", prev, h.Level, h.Text), "Use the next level down so the outline stays navigable.")
				bad[p.Key] = true
			}
			prev = h.Level
			if genericHeading[strings.ToLower(strings.TrimSpace(h.Text))] && h.Level > 1 {
				d.item(p, h.Line, 0.15, fmt.Sprintf("Generic heading %q says little out of context (search results, tables of contents)", h.Text), "Name what the section is about.")
				bad[p.Key] = true
			}
			if s := headingCase(h.Text); s != "" && s != major {
				d.item(p, h.Line, 0.1, fmt.Sprintf("Heading %q is in %s case; most headings use %s case", h.Text, s, major), "Pick one style in the style guide and apply it.")
				bad[p.Key] = true
			}
		}
	}
	keys := make([]string, 0, len(titles))
	for k := range titles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	dups := 0
	for _, k := range keys {
		ps := titles[k]
		if len(ps) < 2 {
			continue
		}
		dups++
		var files []string
		for _, p := range ps {
			files = append(files, fileOf(p))
			bad[p.Key] = true
		}
		for _, p := range ps {
			d.item(p, 1, 0.5, fmt.Sprintf("Title %q is shared by %d pages: %s", p.Title, len(ps), strings.Join(files, ", ")), "Make each title say what is different about the page.")
		}
	}
	d.Score = share(len(a.pages)-len(bad), len(a.pages))
	d.Summary = fmt.Sprintf("%s pages have no title or heading problem", pct(len(a.pages)-len(bad), len(a.pages)))
	d.metric("heading style", "%d sentence case, %d title case", styles["sentence"], styles["title"])
	d.metric("duplicate titles", "%d", dups)
}

func (a *auditor) shape() {
	d := a.dim("page-shape", "Page shape", Structure,
		fmt.Sprintf("Share of pages without a shape problem: under %d words (except landing pages), over %d words, a section over %d words without a subheading, or over 500 words with no headings.", a.opt.ShortWords, a.opt.LongWords, a.opt.MaxSectionWords))
	bad := 0
	var words []int
	for _, p := range a.pages {
		words = append(words, p.Words)
		problem := false
		switch {
		case p.Words < a.opt.ShortWords && !a.isLanding(p) && !p.Draft:
			d.item(p, 1, 0.3, fmt.Sprintf("Only %d words", p.Words), "Merge it into a related page, or expand it so it stands on its own.")
			problem = true
		case p.Words > a.opt.LongWords:
			d.item(p, 1, 0.4, fmt.Sprintf("%d words: long pages are hard to scan and too big for many agent context windows", p.Words), "Split it by task or topic.")
			problem = true
		}
		sub := 0
		for _, h := range p.Headings {
			if h.Level > 1 {
				sub++
			}
		}
		if sub == 0 && p.Words > 500 {
			d.item(p, 1, 0.4, fmt.Sprintf("%d words with no subheadings", p.Words), "Add headings so readers and search can land on a section.")
			problem = true
		} else {
			sec := map[string]int{}
			line := map[string]int{}
			for _, para := range p.Paragraphs {
				sec[para.Section] += len(strings.Fields(para.Text))
				if line[para.Section] == 0 {
					line[para.Section] = para.Line
				}
			}
			var names []string
			for s := range sec {
				names = append(names, s)
			}
			sort.Strings(names)
			for _, s := range names {
				if sec[s] > a.opt.MaxSectionWords {
					label := s
					if label == "" {
						label = "the introduction"
					} else {
						label = "#" + label
					}
					d.item(p, line[s], 0.25, fmt.Sprintf("Section %s runs %d words without a subheading", label, sec[s]), "Break it up with subheadings, lists or a table.")
					problem = true
				}
			}
		}
		if problem {
			bad++
		}
	}
	d.Score = share(len(a.pages)-bad, len(a.pages))
	d.Summary = fmt.Sprintf("%s pages have no shape problem", pct(len(a.pages)-bad, len(a.pages)))
	sort.Ints(words)
	if len(words) > 0 {
		d.metric("words per page", "median %d, 90th percentile %d, longest %d", words[len(words)/2], words[len(words)*9/10], words[len(words)-1])
	}
}

func fieldPresent(p *model.Page, f string) bool {
	switch f {
	case "title":
		return p.Title != ""
	case "description":
		return strings.TrimSpace(p.Description) != ""
	case "tags", "keywords":
		return len(p.Tags) > 0
	case "owner", "owners":
		return p.Owner != ""
	case "type", "content_type":
		return p.Type != ""
	case "audience":
		return len(p.Audience) > 0
	case "version":
		return p.Version != ""
	}
	v, ok := p.FrontMatter[f]
	return ok && v != nil && fmt.Sprint(v) != ""
}

func (a *auditor) metadata() {
	d := a.dim("metadata", "Metadata completeness", Structure,
		fmt.Sprintf("Mean per page of 70%% x share of required fields (%s) plus 30%% x share of recommended fields (%s).",
			strings.Join(a.opt.RequiredMeta, ", "), strings.Join(a.opt.RecommendedMeta, ", ")))
	var total float64
	have := map[string]int{}
	for _, p := range a.pages {
		req, rec := 0, 0
		var missReq, missRec []string
		for _, f := range a.opt.RequiredMeta {
			if fieldPresent(p, f) {
				req++
				have[f]++
			} else {
				missReq = append(missReq, f)
			}
		}
		for _, f := range a.opt.RecommendedMeta {
			if fieldPresent(p, f) {
				rec++
				have[f]++
			} else {
				missRec = append(missRec, f)
			}
		}
		sc := 0.0
		if n := len(a.opt.RequiredMeta); n > 0 {
			sc += 0.7 * float64(req) / float64(n)
		} else {
			sc += 0.7
		}
		if n := len(a.opt.RecommendedMeta); n > 0 {
			sc += 0.3 * float64(rec) / float64(n)
		} else {
			sc += 0.3
		}
		total += sc
		if len(missReq) > 0 {
			d.item(p, 1, 0.6, "Missing required metadata: "+strings.Join(missReq, ", "), "Add it to the front matter.")
		} else if len(missRec) > 0 {
			d.item(p, 1, 0.15, "Missing recommended metadata: "+strings.Join(missRec, ", "), "")
		}
	}
	if len(a.pages) > 0 {
		d.Score = round1(100 * total / float64(len(a.pages)))
	}
	var parts []string
	for _, f := range append(append([]string{}, a.opt.RequiredMeta...), a.opt.RecommendedMeta...) {
		parts = append(parts, fmt.Sprintf("%s %s", f, pct(have[f], len(a.pages))))
		d.metric(f, "%s", pct(have[f], len(a.pages)))
	}
	d.Summary = strings.Join(parts, "; ")
}

func (a *auditor) duplication() {
	d := a.dim("duplication", "Duplication", Structure,
		"Share of pages not involved in an overlap (half or more of the shorter page's five-word sequences repeated on another page) or a duplicated description.")
	involved := map[string]bool{}
	for _, e := range a.g.Edges {
		if e.Type != kg.EOverlaps {
			continue
		}
		from, to := a.s.Page(pageKey(e.From)), a.s.Page(pageKey(e.To))
		if from == nil || to == nil {
			continue
		}
		involved[from.Key], involved[to.Key] = true, true
		d.item(from, 0, 0.5*e.Confidence+0.2, fmt.Sprintf("Overlaps with %s: %s", fileOf(to), e.Span), "Keep one copy and link to it, or move the shared text to a partial.")
	}
	desc := map[string][]*model.Page{}
	for _, p := range a.pages {
		if t := strings.ToLower(strings.TrimSpace(p.Description)); t != "" {
			desc[t] = append(desc[t], p)
		}
	}
	keys := make([]string, 0, len(desc))
	for k := range desc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ps := desc[k]; len(ps) > 1 {
			for _, p := range ps {
				involved[p.Key] = true
				d.item(p, 1, 0.3, fmt.Sprintf("Description is the same on %d pages", len(ps)), "Describe what this page covers that the others don't.")
			}
		}
	}
	d.Score = share(len(a.pages)-len(involved), len(a.pages))
	d.Summary = fmt.Sprintf("%d pages overlap with another page or share its description", len(involved))
}

func (a *auditor) terminology() {
	d := a.dim("terminology", "Terminology", Structure,
		"Share of pages that use preferred terms only (no vocabulary aliases in prose) and tag only with vocabulary terms. Not scored without a vocabulary file.")
	if a.opt.Vocab.Empty() {
		d.Scored = false
		d.Summary = "No vocabulary file: set `vocabulary` in the config to score terminology"
		for _, r := range a.g.Reviews {
			d.item(nil, 0, 0.2, r.Message, "")
		}
		return
	}
	type alias struct {
		re        *regexp.Regexp
		used, pre string
	}
	var aliases []alias
	for _, s := range a.opt.Vocab.Spellings() {
		c, _, isAlias := a.opt.Vocab.Canonical(s)
		if !isAlias {
			continue
		}
		aliases = append(aliases, alias{regexp.MustCompile(`(?i)(^|[^\w-])` + regexp.QuoteMeta(s) + `($|[^\w-])`), s, c})
	}
	bad := map[string]bool{}
	for _, p := range a.pages {
		texts := append([]model.Paragraph{{Text: p.Title, Line: 1}}, p.Paragraphs...)
		for _, h := range p.Headings {
			texts = append(texts, model.Paragraph{Text: h.Text, Line: h.Line})
		}
		for _, al := range aliases {
			for _, t := range texts {
				if al.re.MatchString(t.Text) {
					d.item(p, t.Line, 0.3, fmt.Sprintf("Uses %q; the preferred term is %q", al.used, al.pre), "Use the preferred term, or change the vocabulary.")
					bad[p.Key] = true
					break
				}
			}
		}
		for _, tag := range p.Tags {
			c, known, isAlias := a.opt.Vocab.Canonical(tag)
			switch {
			case !known:
				d.item(p, 1, 0.2, fmt.Sprintf("Tag %q is not in the vocabulary", tag), "Use an existing term, or add it after review.")
				bad[p.Key] = true
			case isAlias:
				d.item(p, 1, 0.2, fmt.Sprintf("Tag %q is an alias of %q", tag, c), "Use the preferred term.")
				bad[p.Key] = true
			}
		}
	}
	for _, r := range a.g.Reviews {
		d.item(nil, 0, 0.2, r.Message, "")
	}
	d.Score = share(len(a.pages)-len(bad), len(a.pages))
	d.Summary = fmt.Sprintf("%s pages use preferred terms and vocabulary tags", pct(len(a.pages)-len(bad), len(a.pages)))
	d.metric("vocabulary terms", "%d", len(a.opt.Vocab.Terms))
	d.metric("suspected duplicate entities", "%d", len(a.g.Reviews))
}

func (a *auditor) navigation() {
	d := a.dim("navigation", "Navigation depth and breadth", Structure,
		fmt.Sprintf("Mean of: share of pages in the navigation (explicit navigation only), share of navigation entries at most %d levels deep, and share of menus with at most %d entries.", a.opt.MaxNavLevels, a.opt.MaxNavChildren))
	nav := a.s.Nav
	if nav == nil || len(nav.Entries) == 0 {
		d.Scored = false
		d.Summary = "No navigation found"
		return
	}
	var parts []float64
	if nav.Explicit {
		in := 0
		for _, p := range a.pages {
			if nav.Contains(p.Key) {
				in++
			} else if !p.Draft {
				d.item(p, 0, 0.35, "Not in the navigation ("+nav.Source+")", "Add it to the navigation, or link to it from a page that is.")
			}
		}
		parts = append(parts, share(in, len(a.pages)))
		d.metric("pages in navigation", "%s", pct(in, len(a.pages)))
	}
	entries, shallow, menus, narrow := 0, 0, 0, 0
	levels := map[int]int{}
	nav.Walk(func(e *crawl.NavEntry, depth int, parent *crawl.NavEntry) {
		entries++
		levels[depth]++
		if depth <= a.opt.MaxNavLevels {
			shallow++
		} else if e.Key != "" {
			if p := a.s.Page(e.Key); p != nil {
				d.item(p, 0, 0.2, fmt.Sprintf("Navigation entry %q is %d levels deep (%s:%d)", e.Title, depth, e.File, e.Line), "Flatten the section, or promote the page.")
			}
		}
		if len(e.Children) > 0 {
			menus++
			if len(e.Children) <= a.opt.MaxNavChildren {
				narrow++
			} else {
				d.Items = append(d.Items, Item{Dimension: d.ID, File: e.File, Line: e.Line, Impact: 0.3,
					Message: fmt.Sprintf("Menu %q has %d entries", firstNonEmpty(e.Title, e.Key), len(e.Children)), Hint: "Group the entries into subsections."})
			}
		}
	})
	if nav.Explicit {
		top := 0
		for range nav.Entries {
			top++
		}
		if top > a.opt.MaxNavChildren {
			menus++
			d.Items = append(d.Items, Item{Dimension: d.ID, File: nav.Source, Impact: 0.3, Message: fmt.Sprintf("The top level has %d entries", top), Hint: "Group them into sections."})
		} else {
			menus++
			narrow++
		}
	}
	parts = append(parts, share(shallow, entries), share(narrow, menus))
	var sum float64
	for _, p := range parts {
		sum += p
	}
	d.Score = round1(sum / float64(len(parts)))
	max := 0
	for l := range levels {
		if l > max {
			max = l
		}
	}
	d.Summary = fmt.Sprintf("navigation from %s: %d entries, %d levels deep at most", nav.Source, entries, max)
	d.metric("entries within depth", "%s", pct(shallow, entries))
	d.metric("menus within breadth", "%s", pct(narrow, menus))
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func (a *auditor) freshness() {
	d := a.dim("ownership-freshness", "Ownership and freshness", Structure,
		fmt.Sprintf("Mean of: share of pages with an owner (front matter or CODEOWNERS) and share changed in the last %d days (git history). A part without data is left out.", a.opt.StaleDays))
	owned, dated, fresh := 0, 0, 0
	cutoff := a.opt.Now.Add(-time.Duration(a.opt.StaleDays) * 24 * time.Hour).Unix()
	for _, p := range a.pages {
		if p.Owner != "" {
			owned++
		}
		if p.LastModified > 0 {
			dated++
			if p.LastModified >= cutoff {
				fresh++
			} else {
				days := int(a.opt.Now.Sub(time.Unix(p.LastModified, 0)).Hours() / 24)
				d.item(p, 0, 0.1+min(0.3, float64(days)/3650), fmt.Sprintf("Last changed %d days ago", days), "Review it, and record the review date even when nothing changes.")
			}
		}
	}
	var parts []float64
	parts = append(parts, share(owned, len(a.pages)))
	d.metric("pages with an owner", "%s", pct(owned, len(a.pages)))
	if owned == 0 {
		d.item(nil, 0, 0.3, "No page has an owner", "Add a CODEOWNERS file or an owner field.")
	}
	if dated > 0 {
		parts = append(parts, share(fresh, dated))
		d.metric("changed in the last year", "%s", pct(fresh, dated))
	} else {
		a.r.Notes = append(a.r.Notes, "No git history was available, so freshness is not scored.")
	}
	var sum float64
	for _, p := range parts {
		sum += p
	}
	d.Score = round1(sum / float64(len(parts)))
	d.Summary = fmt.Sprintf("%s pages have an owner", pct(owned, len(a.pages)))
	if dated > 0 {
		d.Summary += fmt.Sprintf("; %s changed within %d days", pct(fresh, dated), a.opt.StaleDays)
	} else {
		d.Summary += "; no git history, so freshness is unknown"
	}
}

var (
	urlDate  = regexp.MustCompile(`/(19|20)\d\d[/-]\d\d`)
	urlUpper = regexp.MustCompile(`[A-Z]`)
)

func (a *auditor) urls() {
	d := a.dim("urls", "URL quality", Structure,
		"Share of page URLs that are lower case, use hyphens (not underscores, spaces or encoded characters), have no file extension, are at most 80 characters and at most 5 segments deep.")
	ok := 0
	for _, p := range a.pages {
		route := p.Key
		if p.URL != "" {
			if i := strings.Index(p.URL, "://"); i >= 0 {
				if j := strings.Index(p.URL[i+3:], "/"); j >= 0 {
					route = p.URL[i+3+j:]
				}
			}
		} else if p.Path != "" {
			// local: the source path shows case and separators the route key lowercases away
			route = "/" + strings.TrimSuffix(p.Path, pathExt(p.Path))
		}
		var probs []string
		if urlUpper.MatchString(route) {
			probs = append(probs, "capital letters")
		}
		if strings.Contains(route, "_") && !strings.Contains(route, "/_index") {
			probs = append(probs, "underscores")
		}
		if strings.ContainsAny(route, " %") {
			probs = append(probs, "spaces or encoded characters")
		}
		if strings.HasSuffix(route, ".html") || strings.HasSuffix(route, ".htm") || strings.HasSuffix(route, ".php") {
			probs = append(probs, "a file extension")
		}
		if len(route) > 80 {
			probs = append(probs, fmt.Sprintf("%d characters", len(route)))
		}
		if n := strings.Count(strings.Trim(route, "/"), "/") + 1; n > 5 {
			probs = append(probs, fmt.Sprintf("%d segments", n))
		}
		if urlDate.MatchString(route) {
			probs = append(probs, "a date")
		}
		if len(probs) == 0 {
			ok++
			continue
		}
		d.item(p, 0, 0.15, fmt.Sprintf("URL %s has %s", route, strings.Join(probs, ", ")), "Rename with a redirect from the old URL.")
	}
	d.Score = share(ok, len(a.pages))
	d.Summary = fmt.Sprintf("%s URLs are clean", pct(ok, len(a.pages)))
}

func pathExt(p string) string {
	if i := strings.LastIndex(p, "."); i > strings.LastIndex(p, "/") {
		return p[i:]
	}
	return ""
}
