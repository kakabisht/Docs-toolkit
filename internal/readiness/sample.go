package readiness

import (
	"fmt"
	"sort"
	"strings"

	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/model"
)

// Result labels.
const (
	Audit      = "audit"      // every discovered page was checked
	Indicative = "indicative" // a sample was checked: treat the score as a pointer
)

// MinSample is the sample size below which results are always indicative
// and a warning is shown.
const MinSample = 30

// Sample is the set of pages sent to page-level checks.
type Sample struct {
	URLs       []string       `json:"urls"`
	Size       int            `json:"size"`
	Discovered int            `json:"discovered"` // pages found through llms.txt, the sitemap and crawling
	Sources    map[string]int `json:"sources"`    // discovered pages by how they were found
	Sections   map[string]int `json:"sections"`   // sampled pages by top-level section
	Mode       string         `json:"mode"`       // audit or indicative
	Truncated  bool           `json:"truncated"`  // discovery stopped at the page limit
	Warnings   []string       `json:"warnings,omitempty"`
}

// SamplePages picks up to n pages, spread across top-level sections in
// proportion to their size, evenly spaced within each section, with the
// entry page always included. The choice depends only on the set of URLs, so
// the same site gives the same sample. n <= 0 means every page.
func SamplePages(s *crawl.Site, n, maxPages int) Sample {
	sm := Sample{Sources: map[string]int{}, Sections: map[string]int{}}
	var ok []*model.Page
	for _, p := range s.Pages {
		if p.Err != "" || (p.Status != 0 && p.Status >= 400) {
			continue
		}
		ok = append(ok, p)
		sm.Sources[p.Source]++
	}
	sort.Slice(ok, func(i, j int) bool { return ok[i].Key < ok[j].Key })
	sm.Discovered = len(ok)
	sm.Truncated = maxPages > 0 && len(s.Pages) >= maxPages
	chosen := ok
	if n > 0 && n < len(ok) {
		chosen = stratified(ok, n)
	}
	for _, p := range chosen {
		u := p.URL
		if p.FinalURL != "" {
			u = p.FinalURL
		}
		if u == "" {
			u = p.Key
		}
		sm.URLs = append(sm.URLs, u)
		sm.Sections[section(p.Key)]++
	}
	sm.Size = len(chosen)
	sm.Mode = Audit
	if sm.Size < sm.Discovered || sm.Truncated {
		sm.Mode = Indicative
	}
	if sm.Truncated {
		sm.Warnings = append(sm.Warnings, fmt.Sprintf("Discovery stopped at the page limit (%d): the site has more pages than were seen. Raise --max-pages for an audit.", maxPages))
	}
	if sm.Size < MinSample {
		sm.Mode = Indicative
		sm.Warnings = append(sm.Warnings, fmt.Sprintf("Small sample: %d pages. Page-level results may not represent the site.", sm.Size))
	}
	if sm.Sources["llms.txt"] == 0 && sm.Sources["sitemap"] == 0 && s.Mode == crawl.Live {
		sm.Warnings = append(sm.Warnings, "No pages came from llms.txt or a sitemap; the sample relies on crawling from the start page.")
	}
	return sm
}

func section(key string) string {
	k := key
	if i := strings.Index(k, "/"); i > 0 && !strings.HasPrefix(k, "/") {
		k = k[i:] // live keys start with the host
	}
	k = strings.Trim(k, "/")
	if i := strings.Index(k, "/"); i >= 0 {
		k = k[:i]
	}
	if k == "" {
		return "/"
	}
	return k
}

// stratified allocates n slots across sections by largest remainder, at
// least one per section while slots last, then takes evenly spaced pages.
func stratified(pages []*model.Page, n int) []*model.Page {
	groups := map[string][]*model.Page{}
	var names []string
	for _, p := range pages {
		s := section(p.Key)
		if _, ok := groups[s]; !ok {
			names = append(names, s)
		}
		groups[s] = append(groups[s], p)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == "/") != (names[j] == "/") {
			return names[i] == "/" // the entry page always makes the sample
		}
		if len(groups[names[i]]) != len(groups[names[j]]) {
			return len(groups[names[i]]) > len(groups[names[j]])
		}
		return names[i] < names[j]
	})
	alloc := map[string]int{}
	left := n
	// the entry page and one page per section first, biggest sections first
	for _, s := range names {
		if left == 0 {
			break
		}
		alloc[s] = 1
		left--
	}
	type rem struct {
		s string
		r float64
	}
	var rems []rem
	total := len(pages)
	for _, s := range names {
		if left == 0 {
			break
		}
		exact := float64(n) * float64(len(groups[s])) / float64(total)
		extra := int(exact) - alloc[s]
		if extra > 0 {
			extra = min(extra, left, len(groups[s])-alloc[s])
			alloc[s] += extra
			left -= extra
		}
		rems = append(rems, rem{s, exact - float64(int(exact))})
	}
	sort.SliceStable(rems, func(i, j int) bool { return rems[i].r > rems[j].r })
	for left > 0 {
		progress := false
		for _, r := range rems {
			if left == 0 {
				break
			}
			if alloc[r.s] < len(groups[r.s]) {
				alloc[r.s]++
				left--
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	var out []*model.Page
	for _, s := range names {
		g, k := groups[s], alloc[s]
		if k == 0 {
			continue
		}
		step := float64(len(g)) / float64(k)
		for i := 0; i < k; i++ {
			out = append(out, g[int(float64(i)*step)])
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
