// Package check validates a docs set: internal and external links, images,
// navigation, structure and metadata. It only reports; it never edits files.
package check

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/fetch"
	"github.com/docsbot/docsbot/internal/finding"
	"github.com/docsbot/docsbot/internal/model"
	"github.com/docsbot/docsbot/internal/parse"
	"github.com/docsbot/docsbot/internal/vocab"
)

// Options configures a run.
type Options struct {
	Policy         finding.Policy
	Filter         map[string]bool // report only on these files (--changed-only, single file)
	Offline        bool            // skip external URLs
	Client         *fetch.Client
	IgnoreURLs     []*regexp.Regexp // external URLs never fetched (added to the built-in list)
	IgnoreHrefs    []*regexp.Regexp // raw hrefs skipped entirely
	ExternalAnchor bool             // also check #fragments on external HTML pages
	Schema         *jsonschema.Schema
	Vocab          *vocab.Vocab
	TodoPatterns   []*regexp.Regexp
	EntryKeys      []string // pages that are allowed to have no inbound links (home page)
	CheckLocalhost bool     // check localhost URLs too (staging sites, tests)
	Progress       func(done, total int)
}

// DefaultIgnoreURLs are hosts that never resolve publicly: localhost,
// reserved example domains and obvious placeholders.
var DefaultIgnoreURLs = []*regexp.Regexp{
	regexp.MustCompile(`^https?://(localhost|127\.\d+\.\d+\.\d+|0\.0\.0\.0|\[::1\])([:/]|$)`),
	regexp.MustCompile(`^https?://([^/]+\.)?example(\.(com|org|net))?([:/]|$)`),
	regexp.MustCompile(`^https?://[^/]+\.(invalid|test|local|localhost|internal|example)([:/]|$)`),
	regexp.MustCompile(`^https?://[^/]*(<|>|\{|\}|%3C|%7B|your-|my-|YOUR_|<your)`),
}

// DefaultTodo matches leftover markers. Inline code and code blocks are not searched.
var DefaultTodo = []*regexp.Regexp{
	regexp.MustCompile(`\b(TODO|FIXME|TBD|XXX)\b`),
	regexp.MustCompile(`(?i)lorem ipsum`),
	regexp.MustCompile(`\[(?:PLACEHOLDER|INSERT [^\]]+)\]`),
}

// Run checks every page of s and returns sorted findings.
func Run(ctx context.Context, s *crawl.Site, opt Options) []finding.Finding {
	e := &finding.Emitter{Policy: opt.Policy, Filter: opt.Filter}
	if s.Only != "" && e.Filter == nil {
		e.Filter = map[string]bool{s.Only: true}
	}
	c := &checker{s: s, opt: opt, e: e, inbound: map[string]bool{}, external: map[string][]extRef{}}
	c.links(ctx)
	c.navigation()
	c.structure()
	c.metadata()
	return e.Result()
}

type extRef struct {
	file      string
	line, col int
	href      string
	fragment  string
	image     bool
	internal  bool // live mode: an internal page that was not crawled
}

type checker struct {
	s        *crawl.Site
	opt      Options
	e        *finding.Emitter
	inbound  map[string]bool
	external map[string][]extRef
}

// fileOf is the location a page's findings are reported against.
func fileOf(p *model.Page) string {
	if p.RepoPath != "" {
		return p.RepoPath
	}
	if p.FinalURL != "" {
		return p.FinalURL
	}
	return p.URL
}

func (c *checker) ignoredHref(h string) bool {
	for _, re := range c.opt.IgnoreHrefs {
		if re.MatchString(h) {
			return true
		}
	}
	return false
}

func (c *checker) ignoredURL(u string) bool {
	for i, re := range DefaultIgnoreURLs {
		if i == 0 && c.opt.CheckLocalhost {
			continue
		}
		if re.MatchString(u) {
			return true
		}
	}
	for _, re := range c.opt.IgnoreURLs {
		if re.MatchString(u) {
			return true
		}
	}
	return false
}

func (c *checker) links(ctx context.Context) {
	s := c.s
	for _, p := range s.Pages {
		if p.Err != "" {
			continue
		}
		for _, l := range p.Links {
			if l.Nav {
				continue
			}
			file := fileOf(p)
			from := p
			if l.From != "" {
				// Written in a partial: reported where it is written. Inlined
				// includes (Hugo, MkDocs snippets, AsciiDoc) resolve from the
				// including page, as readers see them; MDX imports resolve from
				// the partial's own folder, as Docusaurus does.
				file = l.From
				if l.FromModule {
					rel, err := filepath.Rel(s.Content, l.From)
					if err != nil {
						rel = l.From
					}
					from = &model.Page{Key: p.Key, Path: filepath.ToSlash(rel), Anchors: p.Anchors, URL: p.URL, FinalURL: p.FinalURL}
				}
			}
			c.link(p, from, l, file)
		}
	}
	c.checkExternal(ctx)
	// orphans
	entries := map[string]bool{"/": true}
	for _, k := range c.opt.EntryKeys {
		entries[k] = true
	}
	if s.Base != nil {
		entries[crawl.NormalizeURL(s.Base)] = true
	}
	if s.Nav != nil {
		s.Nav.Walk(func(n *crawl.NavEntry, _ int, _ *crawl.NavEntry) {
			if n.Key != "" {
				c.inbound[n.Key] = true
			}
		})
	}
	for _, p := range s.Pages {
		if p.Err != "" || p.Draft || entries[p.Key] || c.inbound[p.Key] {
			continue
		}
		hint := "Link to it from a related page or add it to the navigation."
		if s.Nav != nil && !s.Nav.Explicit {
			hint = "Link to it from a related page, or add an index page to its folder so it gets a section listing."
		}
		c.e.Add("page-orphan", fileOf(p), 1, 0, "", "No page or navigation entry links to this page", hint)
	}
	// duplicate slugs
	keys := make([]string, 0, len(s.Duplicates))
	for k := range s.Duplicates {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		files := s.Duplicates[k]
		for _, f := range files {
			others := without(files, f)
			c.e.Add("page-duplicate-slug", f, 1, 0, k, fmt.Sprintf("Publishes to %s, as does %s", k, strings.Join(others, ", ")), "Rename one file or set a distinct slug in front matter.")
		}
	}
	// missing includes
	for _, p := range s.Pages {
		for _, inc := range p.Includes {
			if inc.Path == "" && !strings.Contains(inc.Target, "{") && !strings.HasPrefix(inc.Target, "@") {
				c.e.Add("include-missing", fileOf(p), inc.Line, 0, inc.Target, fmt.Sprintf("Included file %q does not exist", inc.Target), "")
			}
		}
	}
}

func without(ss []string, x string) []string {
	var out []string
	for _, s := range ss {
		if s != x {
			out = append(out, s)
		}
	}
	return out
}

func (c *checker) link(p, from *model.Page, l model.Link, file string) {
	s := c.s
	href := strings.TrimSpace(l.Href)
	if href == "" {
		c.e.Add("link-empty", file, l.Line, l.Column, "", "Link has an empty target", "Add the destination or remove the link.")
		return
	}
	if c.ignoredHref(href) {
		return
	}
	if l.Image && strings.TrimSpace(l.Text) == "" {
		c.e.Add("image-missing-alt", file, l.Line, l.Column, href, "Image has no alt text", "Describe what the image shows, for screen readers and agents.")
	}
	t := s.Resolve(from, href)
	missingRule, caseRule := "link-missing-file", "link-case-mismatch"
	if l.Image {
		missingRule, caseRule = "image-missing", "image-case-mismatch"
	}
	switch t.Kind {
	case crawl.KindOther:
		return
	case crawl.KindAnchor:
		if !hasAnchor(p.Anchors, t.Fragment) {
			c.e.Add("link-missing-anchor", file, l.Line, l.Column, href, fmt.Sprintf("No heading or element with id %q on this page", t.Fragment), suggestAnchor(p.Anchors, t.Fragment))
		}
	case crawl.KindInternal, crawl.KindAsset:
		if !t.Exists {
			if s.Mode == crawl.Live && t.URL != "" {
				c.external[t.URL] = append(c.external[t.URL], extRef{file, l.Line, l.Column, href, t.Fragment, l.Image, true})
				return
			}
			what := "a page or file"
			if l.Image {
				what = "an image"
			}
			c.e.Add(missingRule, file, l.Line, l.Column, href, fmt.Sprintf("Links to %s that does not exist: %s", what, href), c.suggestPage(t))
			return
		}
		if t.CaseMismatch {
			c.e.Add(caseRule, file, l.Line, l.Column, href, fmt.Sprintf("%s only matches %s when case is ignored", href, t.Actual), "Match the file name's case exactly; Linux servers and CI are case-sensitive.")
		}
		if t.Page == nil {
			return
		}
		c.inbound[t.Page.Key] = c.inbound[t.Page.Key] || t.Page.Key != p.Key
		if t.ViaAlias {
			c.e.Add("link-redirect", file, l.Line, l.Column, href, fmt.Sprintf("%s is an alias or redirect for %s", href, t.Page.Key), "Link to the canonical page to avoid a redirect hop.")
		}
		if t.Fragment != "" && !hasAnchor(t.Page.Anchors, t.Fragment) {
			c.e.Add("link-missing-anchor", file, l.Line, l.Column, href, fmt.Sprintf("%s has no heading or element with id %q", fileOf(t.Page), t.Fragment), suggestAnchor(t.Page.Anchors, t.Fragment))
		}
		if s.Nav != nil && s.Nav.Explicit && !s.Nav.Contains(t.Page.Key) && !t.Page.Draft {
			c.e.Add("link-not-in-nav", file, l.Line, l.Column, href, fmt.Sprintf("Links to %s, which is not in the navigation (%s)", fileOf(t.Page), s.Nav.Source), "Add the page to the navigation, or link to a page that is.")
		}
	case crawl.KindExternal:
		if c.opt.Offline || c.ignoredURL(t.URL) {
			return
		}
		c.external[t.URL] = append(c.external[t.URL], extRef{file, l.Line, l.Column, href, t.Fragment, l.Image, false})
	}
}

func hasAnchor(anchors map[string]bool, frag string) bool {
	if anchors[frag] {
		return true
	}
	if f, err := url.PathUnescape(frag); err == nil && anchors[f] {
		return true
	}
	return frag == "top"
}

// suggestAnchor names the closest existing anchor, if any is close.
func suggestAnchor(anchors map[string]bool, frag string) string {
	best, bestD := "", 1<<30
	for a := range anchors {
		if d := levenshtein(strings.ToLower(a), strings.ToLower(frag)); d < bestD || (d == bestD && a < best) {
			best, bestD = a, d
		}
	}
	if best != "" && bestD <= max(2, len(frag)/3) {
		return fmt.Sprintf("Did you mean #%s?", best)
	}
	return "Check the heading text; anchors are generated from it."
}

func (c *checker) suggestPage(t crawl.Target) string {
	want := path.Base(strings.TrimSuffix(t.URL, "/"))
	if want == "" || want == "." || want == "/" {
		want = path.Base(t.Key)
	}
	want = strings.TrimSuffix(want, path.Ext(want))
	best, bestD := "", 1<<30
	for _, p := range c.s.Pages {
		name := path.Base(p.Key)
		if d := levenshtein(strings.ToLower(name), strings.ToLower(want)); d < bestD || (d == bestD && fileOf(p) < best) {
			best, bestD = fileOf(p), d
		}
	}
	if best != "" && bestD <= max(2, len(want)/3) {
		return "Did you mean " + best + "?"
	}
	return "Fix the path, or remove the link if the page was deleted."
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// checkExternal fetches every distinct external URL once, with the client's
// per-host limits, retries and cache.
func (c *checker) checkExternal(ctx context.Context) {
	if len(c.external) == 0 || c.opt.Client == nil {
		return
	}
	urls := make([]string, 0, len(c.external))
	for u := range c.external {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	needBody := map[string]bool{}
	if c.opt.ExternalAnchor {
		for _, u := range urls {
			for _, r := range c.external[u] {
				if r.fragment != "" && !r.internal && checkableFragment(r.fragment) {
					needBody[u] = true
				}
			}
		}
	}
	results := make([]*fetch.Response, len(urls))
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	jobs := make(chan int)
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				if needBody[urls[i]] {
					results[i] = c.opt.Client.Get(ctx, urls[i])
				} else {
					results[i] = c.opt.Client.Check(ctx, urls[i])
				}
				mu.Lock()
				done++
				d := done
				mu.Unlock()
				if c.opt.Progress != nil {
					c.opt.Progress(d, len(urls))
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
		if r == nil {
			continue
		}
		var anchors map[string]bool
		if needBody[u] && r.OK() && strings.Contains(strings.ToLower(r.ContentType), "html") {
			anchors = parse.HTML(r.Body).Anchors
		}
		for _, ref := range c.external[u] {
			c.classify(u, r, ref, anchors)
		}
	}
}

func checkableFragment(f string) bool {
	// line anchors and generated ids on code hosts are added by scripts
	return !regexp.MustCompile(`^(L\d+|issuecomment-|diff-|:~:)`).MatchString(f)
}

func (c *checker) classify(u string, r *fetch.Response, ref extRef, anchors map[string]bool) {
	add := func(rule, msg, hint string) {
		if ref.internal {
			switch rule {
			case "link-external-broken":
				rule = "link-missing-file"
				if ref.image {
					rule = "image-missing"
				}
			case "link-external-redirect":
				rule = "link-redirect"
			}
		}
		c.e.Add(rule, ref.file, ref.line, ref.col, ref.href, msg, hint)
	}
	switch {
	case r.Status == 404 || r.Status == 410:
		add("link-external-broken", fmt.Sprintf("%s returns %d", u, r.Status), "Update the link to the page's new address, or remove it.")
	case r.Status >= 500:
		add("link-external-server-error", fmt.Sprintf("%s returns %d (after %d retries)", u, r.Status, r.Retried), "Often temporary; the scheduled full run will confirm.")
	case r.Status == 401 || r.Status == 403 || r.Status == 429 || r.Status == 999:
		add("link-external-unverified", fmt.Sprintf("%s refused an automated check (%d)", u, r.Status), "Bot protection, not necessarily a broken link. Open it in a browser to confirm.")
	case r.Status == 0 && r.Timeout():
		add("link-external-timeout", fmt.Sprintf("%s timed out", u), "Check the URL; the site may be slow or down.")
	case r.Status == 0:
		add("link-external-timeout", fmt.Sprintf("%s could not be reached: %s", u, shortErr(r.Err)), "Check the host name; DNS or the connection failed.")
	case r.Status >= 400:
		add("link-external-unverified", fmt.Sprintf("%s returns %d", u, r.Status), "Unusual status for a link check; verify by hand.")
	default:
		if len(r.Chain) > 0 && (r.Chain[0].Status == 301 || r.Chain[0].Status == 308) && !trivialRedirect(u, r.FinalURL) {
			msg := fmt.Sprintf("%s permanently redirects to %s", u, r.FinalURL)
			if len(r.Chain) > 1 {
				msg = fmt.Sprintf("%s redirects %d times, ending at %s", u, len(r.Chain), r.FinalURL)
			}
			add("link-external-redirect", msg, "Update the link to the final URL.")
		}
		if anchors != nil && ref.fragment != "" && checkableFragment(ref.fragment) && !hasAnchor(anchors, ref.fragment) {
			add("link-external-anchor", fmt.Sprintf("%s has no element with id %q", u, ref.fragment), "The section may have been renamed; anchors added by scripts can't be seen.")
		}
	}
}

func shortErr(s string) string {
	if i := strings.LastIndex(s, ": "); i >= 0 {
		return s[i+2:]
	}
	return s
}

// trivialRedirect ignores http to https, trailing-slash and www redirects.
func trivialRedirect(from, to string) bool {
	a, err1 := url.Parse(from)
	b, err2 := url.Parse(to)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.EqualFold(strings.TrimPrefix(a.Host, "www."), strings.TrimPrefix(b.Host, "www.")) &&
		strings.TrimSuffix(a.Path, "/") == strings.TrimSuffix(b.Path, "/") && a.RawQuery == b.RawQuery
}

func (c *checker) navigation() {
	nav := c.s.Nav
	if nav == nil {
		return
	}
	nav.Walk(func(n *crawl.NavEntry, _ int, _ *crawl.NavEntry) {
		if n.Missing {
			c.e.Add("nav-missing-target", n.File, n.Line, 0, n.Target, fmt.Sprintf("Navigation entry %q points to %s, which does not exist", n.Title, n.Target), "Fix the path or remove the entry.")
		}
	})
}
