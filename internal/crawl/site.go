// Package crawl discovers and parses docs pages from a live site or a local
// repo, and resolves links between them. Every command builds on its Site.
package crawl

import (
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/docsbot/docsbot/internal/model"
	"github.com/docsbot/docsbot/internal/parse"
)

// Mode is where a Site was read from.
type Mode string

const (
	Live  Mode = "live"
	Local Mode = "local"
)

// Site is the parsed set of pages from one docs source.
type Site struct {
	Mode    Mode
	Source  string   // URL or directory given by the user
	Base    *url.URL // live site base (live mode, or local mode with base_url)
	Root    string   // local: repo directory
	Content string   // local: content root directory
	Static  []string // local: directories searched for assets
	// Generator is the detected static site generator (hugo, docusaurus, mkdocs ...), if any.
	Generator string
	mdOpts    parse.Options
	Pages     []*model.Page
	LLMSTxt   model.LLMSTxt
	Sitemap   []string // page URLs listed in the sitemap (live mode)

	byKey   map[string]*model.Page
	byPath  map[string]*model.Page
	aliases map[string]*model.Page
	files   map[string]bool // local: every file under the repo, relative to Root, lowercased
}

func (s *Site) index() {
	s.byKey = map[string]*model.Page{}
	s.byPath = map[string]*model.Page{}
	s.aliases = map[string]*model.Page{}
	sort.Slice(s.Pages, func(i, j int) bool { return s.Pages[i].Key < s.Pages[j].Key })
	for _, p := range s.Pages {
		s.byKey[p.Key] = p
		if p.Path != "" {
			s.byPath[strings.ToLower(p.Path)] = p
		}
		for _, a := range p.Aliases {
			s.aliases[s.keyFor(a)] = p
		}
	}
}

// Page returns the page with the given key.
func (s *Site) Page(key string) *model.Page { return s.byKey[key] }

// LinkKind classifies a resolved link.
type LinkKind string

const (
	KindInternal LinkKind = "internal" // another docs page
	KindAnchor   LinkKind = "anchor"   // a section on the same page
	KindAsset    LinkKind = "asset"    // image or download inside the docs
	KindExternal LinkKind = "external" // another site
	KindOther    LinkKind = "other"    // mailto:, tel:, javascript:, templated
)

// Target is a link resolved against the page it appears on.
type Target struct {
	Kind     LinkKind
	Key      string // page key for internal/anchor links
	Fragment string
	URL      string // absolute URL (live internal, external), or file path for local assets
	Page     *model.Page
	Exists   bool // target page/asset found (internal, anchor, asset)
	ViaAlias bool // matched through an alias or redirect rather than the canonical address
}

// Resolve classifies href as it appears on page p.
func (s *Site) Resolve(p *model.Page, href string) Target {
	href = strings.TrimSpace(href)
	if href == "" || strings.Contains(href, "{{") || strings.Contains(href, "{%") || strings.HasPrefix(href, "{") {
		return Target{Kind: KindOther}
	}
	u, err := url.Parse(href)
	if err != nil {
		return Target{Kind: KindExternal, URL: href}
	}
	switch strings.ToLower(u.Scheme) {
	case "", "http", "https":
	default:
		return Target{Kind: KindOther, URL: href}
	}
	frag := u.Fragment
	if u.Scheme == "" && u.Host == "" && u.Path == "" {
		if frag == "" {
			return Target{Kind: KindOther}
		}
		return Target{Kind: KindAnchor, Key: p.Key, Fragment: frag, Page: p, Exists: true}
	}
	if s.Mode == Live {
		return s.resolveLive(p, u)
	}
	return s.resolveLocal(p, u)
}

func (s *Site) resolveLive(p *model.Page, u *url.URL) Target {
	base := p.FinalURL
	if base == "" {
		base = p.URL
	}
	bu, _ := url.Parse(base)
	abs := bu.ResolveReference(u)
	if !s.isInternalURL(abs) {
		abs.Fragment = ""
		return Target{Kind: KindExternal, URL: abs.String(), Fragment: u.Fragment}
	}
	frag := abs.Fragment
	abs.Fragment = ""
	key := NormalizeURL(abs)
	t := Target{Kind: KindInternal, Key: key, Fragment: frag, URL: abs.String()}
	if isAssetPath(abs.Path) {
		t.Kind = KindAsset
	}
	if pg := s.byKey[key]; pg != nil {
		t.Page, t.Exists, t.Kind = pg, true, KindInternal
	} else if pg := s.aliases[key]; pg != nil {
		t.Page, t.Exists, t.ViaAlias, t.Kind = pg, true, true, KindInternal
	}
	return t
}

func (s *Site) isInternalURL(u *url.URL) bool {
	if s.Base == nil {
		return false
	}
	if !strings.EqualFold(stripWWW(u.Host), stripWWW(s.Base.Host)) {
		return false
	}
	bp := strings.TrimSuffix(s.Base.Path, "/")
	return bp == "" || u.Path == bp || strings.HasPrefix(u.Path, bp+"/")
}

func stripWWW(h string) string { return strings.TrimPrefix(strings.ToLower(h), "www.") }

func (s *Site) resolveLocal(p *model.Page, u *url.URL) Target {
	frag := u.Fragment
	var route string
	switch {
	case u.Scheme != "" || u.Host != "":
		if !s.isInternalURL(u) {
			c := *u
			c.Fragment = ""
			return Target{Kind: KindExternal, URL: c.String(), Fragment: frag}
		}
		route = strings.TrimPrefix(u.Path, strings.TrimSuffix(s.Base.Path, "/"))
	case strings.HasPrefix(u.Path, "/"):
		route = u.Path
		if s.Base != nil {
			if bp := strings.TrimSuffix(s.Base.Path, "/"); bp != "" && strings.HasPrefix(route, bp+"/") {
				route = strings.TrimPrefix(route, bp)
			}
		}
	default:
		// Relative link: first as a file path next to the source file,
		// then as a URL relative to the page's published route.
		dir := path.Dir(p.Path)
		fp := path.Clean(path.Join(dir, u.Path))
		if pg := s.pageByFile(fp); pg != nil {
			return Target{Kind: KindInternal, Key: pg.Key, Fragment: frag, Page: pg, Exists: true}
		}
		if isAssetPath(u.Path) {
			return s.asset(fp, u.Path, p)
		}
		// pretty URLs (/dir/name/) first, then plain ones (/dir/name.html)
		for _, route := range []string{path.Join(p.Key+"/", u.Path), path.Join(path.Dir(p.Key), u.Path)} {
			if pg, alias := s.pageByRoute(route); pg != nil {
				return Target{Kind: KindInternal, Key: pg.Key, Fragment: frag, Page: pg, Exists: true, ViaAlias: alias}
			}
		}
		// Hugo's link render hook resolves page-relative paths with GetPage, which
		// falls back to the content root.
		if s.Generator == "hugo" {
			if pg, alias := s.pageByRoute("/" + u.Path); pg != nil {
				return Target{Kind: KindInternal, Key: pg.Key, Fragment: frag, Page: pg, Exists: true, ViaAlias: alias}
			}
		}
		// report the file-relative reading; it's what an author most likely meant
		return Target{Kind: KindInternal, Key: RouteKey(fp), Fragment: frag, URL: fp}
	}
	if isAssetPath(route) {
		return s.asset(strings.TrimPrefix(route, "/"), route, p)
	}
	if pg, alias := s.pageByRoute(route); pg != nil {
		return Target{Kind: KindInternal, Key: pg.Key, Fragment: frag, Page: pg, Exists: true, ViaAlias: alias}
	}
	// absolute path to a source file, e.g. /docs/intro.md
	if pg := s.pageByFile(strings.TrimPrefix(route, "/")); pg != nil {
		return Target{Kind: KindInternal, Key: pg.Key, Fragment: frag, Page: pg, Exists: true}
	}
	t := Target{Kind: KindInternal, Key: RouteKey(route), Fragment: frag, URL: route}
	// a static file served at this route (e.g. /llms.txt, /api/openapi.yaml)
	if s.staticFile(strings.TrimPrefix(route, "/")) {
		t.Kind, t.Exists = KindAsset, true
	}
	return t
}

func (s *Site) asset(rel, display string, p *model.Page) Target {
	t := Target{Kind: KindAsset, URL: display}
	if s.staticFile(rel) {
		t.Exists = true
	}
	// Hugo page bundles: assets sit next to index.md
	if !t.Exists && p != nil {
		if s.staticFile(path.Join(path.Dir(p.Path), path.Base(rel))) {
			t.Exists = true
		}
	}
	return t
}

// staticFile reports whether rel exists under the content root or a static dir.
func (s *Site) staticFile(rel string) bool {
	rel = strings.ToLower(path.Clean(strings.TrimPrefix(rel, "/")))
	if s.files == nil {
		return false
	}
	for _, d := range append([]string{s.Content}, s.Static...) {
		if s.files[strings.ToLower(path.Join(d, rel))] {
			return true
		}
	}
	return false
}

var mdExts = []string{".md", ".mdx", ".markdown", ".html", ".htm"}

func (s *Site) pageByFile(fp string) *model.Page {
	fp = strings.ToLower(strings.TrimPrefix(path.Clean(fp), "/"))
	if pg := s.byPath[fp]; pg != nil {
		return pg
	}
	if path.Ext(fp) == "" || fp == "." {
		for _, ext := range mdExts {
			if pg := s.byPath[fp+ext]; pg != nil {
				return pg
			}
		}
		for _, idx := range []string{"_index", "index", "readme"} {
			for _, ext := range mdExts {
				if pg := s.byPath[path.Join(fp, idx+ext)]; pg != nil {
					return pg
				}
			}
		}
	}
	return nil
}

func (s *Site) pageByRoute(route string) (*model.Page, bool) {
	k := RouteKey(route)
	if pg := s.byKey[k]; pg != nil {
		return pg, false
	}
	if pg := s.aliases[k]; pg != nil {
		return pg, true
	}
	// /guide/intro.html or /guide/intro.md written against a clean-URL site
	for _, ext := range mdExts {
		if strings.HasSuffix(k, ext) {
			if pg := s.byKey[strings.TrimSuffix(k, ext)]; pg != nil {
				return pg, false
			}
		}
	}
	return nil, false
}

func (s *Site) keyFor(ref string) string {
	if s.Mode == Live {
		if u, err := url.Parse(ref); err == nil {
			if s.Base != nil {
				u = s.Base.ResolveReference(u)
			}
			return NormalizeURL(u)
		}
	}
	if u, err := url.Parse(ref); err == nil && u.Host != "" {
		ref = u.Path
	}
	return RouteKey(ref)
}

// RouteKey normalizes a site path: lowercase, leading slash, no trailing slash,
// no index.html.
func RouteKey(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.ToLower(path.Clean("/" + p))
	for _, idx := range []string{"/index.html", "/index.htm", "/index"} {
		if strings.HasSuffix(p, idx) {
			p = strings.TrimSuffix(p, idx)
		}
	}
	if p == "" || p == "." {
		p = "/"
	}
	return p
}

// NormalizeURL is the page key for a live URL: host + RouteKey(path), with
// .html and .md extensions removed so a page and its markdown twin match.
func NormalizeURL(u *url.URL) string {
	p := strings.ToLower(u.Path)
	for _, ext := range []string{".html", ".htm", ".md", ".mdx"} {
		if strings.HasSuffix(p, ext) {
			p = strings.TrimSuffix(p, ext)
			break
		}
	}
	return stripWWW(u.Host) + RouteKey(p)
}

var assetExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".webp": true, ".avif": true, ".ico": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".mp4": true, ".webm": true, ".mov": true, ".mp3": true,
	".json": true, ".yaml": true, ".yml": true, ".txt": true, ".csv": true, ".xml": true, ".css": true, ".js": true,
	".woff": true, ".woff2": true, ".ttf": true,
}

func isAssetPath(p string) bool { return assetExts[strings.ToLower(path.Ext(p))] }

// IsIndexFile reports whether p is a section index (_index.md, index.md, README.md).
func IsIndexFile(p string) bool {
	b := strings.ToLower(strings.TrimSuffix(path.Base(p), path.Ext(p)))
	return b == "_index" || b == "index" || b == "readme"
}
