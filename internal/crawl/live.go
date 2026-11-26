package crawl

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/kakabisht/Docs-toolkit/internal/fetch"
	"github.com/kakabisht/Docs-toolkit/internal/model"
	"github.com/kakabisht/Docs-toolkit/internal/parse"
)

// LiveOptions configures LoadLive.
type LiveOptions struct {
	MaxPages int
	Follow   bool // also crawl internal links found in pages
	Workers  int
	Include  []string // globs matched against the URL path
	Exclude  []string
	Progress func(done, queued int)
}

// LoadLive discovers pages from llms.txt, robots.txt/sitemap.xml and (optionally)
// internal links, then fetches and parses them.
func LoadLive(ctx context.Context, c *fetch.Client, base string, opt LiveOptions) (*Site, error) {
	bu, err := url.Parse(base)
	if err != nil || bu.Host == "" {
		return nil, fmt.Errorf("invalid URL %q", base)
	}
	if bu.Path == "" {
		bu.Path = "/"
	}
	s := &Site{Mode: Live, Source: base, Base: bu}
	if opt.Workers <= 0 {
		opt.Workers = 8
	}
	if opt.MaxPages <= 0 {
		opt.MaxPages = 2000
	}

	// 1. llms.txt: under the base path first, then at the origin.
	for _, cand := range llmsCandidates(bu) {
		r := c.Get(ctx, cand)
		if r.OK() && !looksHTML(r) {
			s.LLMSTxt = model.LLMSTxt{Location: cand, Found: true, Size: len(r.Body), Links: parse.LLMSTxtLinks(r.Body)}
			break
		}
	}
	if !s.LLMSTxt.Found {
		s.LLMSTxt.Location = llmsCandidates(bu)[0]
	}

	// 2. sitemap, from robots.txt or the default location.
	origin := &url.URL{Scheme: bu.Scheme, Host: bu.Host}
	var maps []string
	if r := c.Get(ctx, origin.String()+"/robots.txt"); r.OK() {
		maps = parse.RobotsSitemaps(r.Body)
	}
	if len(maps) == 0 {
		maps = []string{origin.String() + "/sitemap.xml"}
		if p := strings.TrimSuffix(bu.Path, "/"); p != "" {
			maps = append([]string{origin.String() + p + "/sitemap.xml"}, maps...)
		}
	}
	seenMap := map[string]bool{}
	for i := 0; i < len(maps) && i < 50; i++ {
		if seenMap[maps[i]] {
			continue
		}
		seenMap[maps[i]] = true
		r := c.Get(ctx, maps[i])
		if !r.OK() {
			continue
		}
		pages, nested := parse.Sitemap(r.Body)
		maps = append(maps, nested...)
		for _, p := range pages {
			if pu, err := url.Parse(p); err == nil && s.isInternalURL(pu) {
				s.Sitemap = append(s.Sitemap, p)
			}
		}
	}

	// 3. seed the crawl. Sitemap URLs first: they are what humans and links use;
	// llms.txt often points at .md twins of the same pages.
	type job struct{ url, source string }
	var seeds []job
	for _, p := range s.Sitemap {
		seeds = append(seeds, job{p, "sitemap"})
	}
	for _, h := range s.LLMSTxt.Links {
		ref, err := url.Parse(h)
		if err != nil {
			continue
		}
		abs := mustParse(s.LLMSTxt.Location).ResolveReference(ref)
		abs.Fragment = ""
		if s.isInternalURL(abs) {
			seeds = append(seeds, job{abs.String(), "llms.txt"})
		}
	}
	seeds = append(seeds, job{bu.String(), "crawl"})

	var (
		mu      sync.Mutex
		queued  = map[string]bool{}
		queue   = make(chan job, opt.MaxPages+len(seeds))
		pending sync.WaitGroup
		count   int
		done    int
	)
	enqueue := func(j job) {
		u, err := url.Parse(j.url)
		if err != nil {
			return
		}
		u.Fragment = ""
		if isAssetPath(u.Path) {
			return
		}
		if !keep(strings.TrimPrefix(u.Path, "/"), opt.Include, opt.Exclude) {
			return
		}
		k := NormalizeURL(u)
		mu.Lock()
		defer mu.Unlock()
		if queued[k] || count >= opt.MaxPages {
			return
		}
		queued[k] = true
		count++
		pending.Add(1)
		queue <- job{u.String(), j.source}
	}
	for _, j := range seeds {
		enqueue(j)
	}

	go func() { pending.Wait(); close(queue) }()
	var wg sync.WaitGroup
	for w := 0; w < opt.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				pg := fetchPage(ctx, c, j.url, j.source)
				mu.Lock()
				s.Pages = append(s.Pages, pg)
				done++
				d, q := done, count
				mu.Unlock()
				if opt.Progress != nil {
					opt.Progress(d, q)
				}
				if opt.Follow && pg.Err == "" {
					base := mustParse(pg.FinalURL)
					for _, l := range pg.Links {
						if l.Image {
							continue
						}
						ref, err := url.Parse(l.Href)
						if err != nil || (ref.Scheme != "" && ref.Scheme != "http" && ref.Scheme != "https") {
							continue
						}
						abs := base.ResolveReference(ref)
						if s.isInternalURL(abs) {
							enqueue(job{abs.String(), "crawl"})
						}
					}
				}
				pending.Done()
			}
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// pages whose redirect landed on an already-fetched page are aliases, not new nodes
	s.Pages = mergeRedirects(s.Pages)
	s.index()
	return s, nil
}

func fetchPage(ctx context.Context, c *fetch.Client, u, source string) *model.Page {
	r := c.Get(ctx, u)
	var pg *model.Page
	ct := strings.ToLower(r.ContentType)
	switch {
	case r.Err != "" || !r.OK():
		pg = &model.Page{Format: "unknown", Anchors: map[string]bool{}}
		if err := fetch.ErrStatus(r); err != nil {
			pg.Err = err.Error()
		}
	case strings.Contains(ct, "markdown") || strings.HasSuffix(strings.ToLower(mustParse(r.FinalURL).Path), ".md") || (strings.Contains(ct, "text/plain") && !looksHTML(r)):
		pg = parse.Markdown(r.Body)
	default:
		pg = parse.HTML(r.Body)
	}
	pg.URL = u
	pg.FinalURL = r.FinalURL
	pg.Status = r.Status
	pg.Source = source
	// key pages by where they ended up; the requested URL becomes an alias
	pg.Key = NormalizeURL(mustParse(r.FinalURL))
	if pg.Key != NormalizeURL(mustParse(u)) {
		pg.Aliases = append(pg.Aliases, u)
	}
	return pg
}

// mergeRedirects folds pages that redirected to the same final URL into one.
func mergeRedirects(pages []*model.Page) []*model.Page {
	byKey := map[string]*model.Page{}
	var out []*model.Page
	for _, p := range pages {
		if t, ok := byKey[p.Key]; ok {
			aliases := append(append([]string(nil), t.Aliases...), p.Aliases...)
			if t.Redirected() && !p.Redirected() {
				*t = *p // prefer the copy fetched without a redirect
			}
			t.Aliases = aliases
			continue
		}
		byKey[p.Key] = p
		out = append(out, p)
	}
	return out
}

func llmsCandidates(bu *url.URL) []string {
	origin := bu.Scheme + "://" + bu.Host
	var c []string
	if p := strings.TrimSuffix(bu.Path, "/"); p != "" {
		c = append(c, origin+p+"/llms.txt")
	}
	return append(c, origin+"/llms.txt")
}

func looksHTML(r *fetch.Response) bool {
	if strings.Contains(strings.ToLower(r.ContentType), "html") {
		return true
	}
	head := strings.ToLower(strings.TrimSpace(string(r.Body[:min(len(r.Body), 512)])))
	return strings.HasPrefix(head, "<!doctype html") || strings.HasPrefix(head, "<html")
}

func mustParse(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		return &url.URL{}
	}
	return u
}
