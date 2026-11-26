package crawl

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/docsbot/docsbot/internal/model"
	"github.com/docsbot/docsbot/internal/parse"
)

// LocalOptions configures LoadLocal.
type LocalOptions struct {
	ContentDir string // relative to the repo root; auto-detected when empty
	StaticDirs []string
	BaseURL    string
	Include    []string
	Exclude    []string
	MaxPages   int
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".next": true, ".docusaurus": true, "build": true, "dist": true, "public": true, "resources": true, "_site": true, "site": true}

// LoadLocal reads a docs repo from disk.
func LoadLocal(root string, opt LocalOptions) (*Site, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	s := &Site{Mode: Local, Source: root, Root: root, files: map[string]bool{}}
	if opt.BaseURL != "" {
		if s.Base, err = url.Parse(opt.BaseURL); err != nil {
			return nil, fmt.Errorf("base_url: %w", err)
		}
	}
	s.Content = opt.ContentDir
	if s.Content == "" {
		s.Content = detectContent(root)
	}
	s.Content = filepath.ToSlash(path.Clean(s.Content))
	s.Static = opt.StaticDirs
	if len(s.Static) == 0 {
		for _, d := range []string{"static", "public", "assets", "docs/public", "docs/static", "docs/assets", "."} {
			if fi, err := os.Stat(filepath.Join(root, d)); err == nil && fi.IsDir() {
				s.Static = append(s.Static, d)
			}
		}
	}
	contentAbs := filepath.Join(root, s.Content)
	s.Generator = detectGenerator(root)
	if s.Generator == "hugo" {
		s.mdOpts.DefinitionTermIDs = hugoTermIDs(root)
	}

	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) && !isUnder(rel, s.Content) && !isStatic(rel, s.Static) {
				return filepath.SkipDir
			}
			return nil
		}
		s.files[strings.ToLower(rel)] = true
		return nil
	})
	if err != nil {
		return nil, err
	}

	err = filepath.WalkDir(contentAbs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// "_" folders hold partials and includes that are never published (Hugo _common, Docusaurus _partials)
			if p != contentAbs && (d.Name() == "node_modules" || strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), "_") && !IsIndexFile(d.Name()) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".md" && ext != ".mdx" && ext != ".markdown" && ext != ".html" && ext != ".htm" {
			return nil
		}
		rel, _ := filepath.Rel(contentAbs, p)
		rel = filepath.ToSlash(rel)
		if !keep(rel, opt.Include, opt.Exclude) {
			return nil
		}
		if opt.MaxPages > 0 && len(s.Pages) >= opt.MaxPages {
			return filepath.SkipAll
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var pg *model.Page
		var fm parse.FrontMatter
		if ext == ".html" || ext == ".htm" {
			pg = parse.HTML(b)
		} else {
			fm, _, _ = parse.SplitFrontMatter(b)
			pg = parse.MarkdownWith(b, s.mdOpts)
			s.mergeIncludes(pg, b, contentAbs, 0)
		}
		pg.Path = rel
		pg.Source = "local"
		pg.Key = localRoute(rel, fm)
		if s.Base != nil {
			u := *s.Base
			u.Path = strings.TrimSuffix(s.Base.Path, "/") + pg.Key
			pg.URL = u.String()
		}
		if pg.Title == "" {
			pg.Title = fm.LinkTitle
		}
		s.Pages = append(s.Pages, pg)
		return nil
	})
	if err != nil {
		return nil, err
	}
	dedupeKeys(s.Pages)
	s.index()
	s.LLMSTxt = s.localLLMSTxt()
	return s, nil
}

func isUnder(rel, dir string) bool {
	return dir == "." || rel == dir || strings.HasPrefix(rel, dir+"/") || strings.HasPrefix(dir, rel+"/")
}

func isStatic(rel string, dirs []string) bool {
	for _, d := range dirs {
		if d != "." && isUnder(rel, d) {
			return true
		}
	}
	return false
}

// detectContent finds the content root of common docs generators.
func detectContent(root string) string {
	for _, c := range []string{"content/en", "content/docs", "content", "docs/content", "src/content/docs", "docs", "doc", "documentation", "pages", "src/pages"} {
		if fi, err := os.Stat(filepath.Join(root, c)); err == nil && fi.IsDir() {
			return c
		}
	}
	return "."
}

// localRoute is the published path of a source file, honoring url and slug front matter.
func localRoute(rel string, fm parse.FrontMatter) string {
	if fm.URL != "" {
		return RouteKey(fm.URL)
	}
	if fm.Permalink != "" {
		return RouteKey(fm.Permalink)
	}
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	name := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	if IsIndexFile(rel) {
		return RouteKey("/" + dir)
	}
	if fm.Slug != "" {
		name = fm.Slug
	}
	return RouteKey("/" + path.Join(dir, name))
}

// dedupeKeys keeps keys unique when two files map to one route (foo.md + foo/index.md).
func dedupeKeys(pages []*model.Page) {
	seen := map[string]int{}
	for _, p := range pages {
		if n := seen[p.Key]; n > 0 {
			p.Key = fmt.Sprintf("%s~%d", p.Key, n)
		}
		seen[p.Key]++
	}
}

func keep(rel string, include, exclude []string) bool {
	if len(include) > 0 {
		ok := false
		for _, g := range include {
			if match(g, rel) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, g := range exclude {
		if match(g, rel) {
			return false
		}
	}
	return true
}

// match supports path.Match globs plus a "**/" prefix or "/**" suffix.
func match(glob, rel string) bool {
	if strings.HasSuffix(glob, "/**") {
		prefix := strings.TrimSuffix(glob, "/**")
		return rel == prefix || strings.HasPrefix(rel, prefix+"/")
	}
	if strings.HasPrefix(glob, "**/") {
		g := strings.TrimPrefix(glob, "**/")
		parts := strings.Split(rel, "/")
		for i := range parts {
			if ok, _ := path.Match(g, strings.Join(parts[i:], "/")); ok {
				return true
			}
		}
		return false
	}
	ok, _ := path.Match(glob, rel)
	return ok
}

func (s *Site) localLLMSTxt() model.LLMSTxt {
	for _, c := range append([]string{s.Content}, s.Static...) {
		rel := path.Join(c, "llms.txt")
		if !s.files[strings.ToLower(rel)] {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Root, rel))
		if err != nil {
			continue
		}
		return model.LLMSTxt{Location: rel, Found: true, Size: len(b), Links: parse.LLMSTxtLinks(b)}
	}
	return model.LLMSTxt{Location: "the content root or static folders"}
}

// detectGenerator recognizes static site generators whose link rules differ.
func detectGenerator(root string) string {
	checks := []struct {
		gen   string
		files []string
	}{
		{"hugo", []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config/_default/hugo.toml"}},
		{"docusaurus", []string{"docusaurus.config.js", "docusaurus.config.ts"}},
		{"mkdocs", []string{"mkdocs.yml", "mkdocs.yaml"}},
		{"astro", []string{"astro.config.mjs", "astro.config.ts"}},
		{"mintlify", []string{"mint.json", "docs.json"}},
		{"jekyll", []string{"_config.yml"}},
	}
	for _, c := range checks {
		for _, f := range c.files {
			if _, err := os.Stat(filepath.Join(root, f)); err == nil {
				return c.gen
			}
		}
	}
	return ""
}

var termIDRe = regexp.MustCompile(`(?i)autoDefinitionTermID["']?\s*[:=]\s*true`)

func hugoTermIDs(root string) bool {
	for _, f := range []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config.toml", "config/_default/hugo.toml", "config/_default/markup.toml"} {
		if b, err := os.ReadFile(filepath.Join(root, f)); err == nil && termIDRe.Match(b) {
			return true
		}
	}
	return false
}

// includeRe matches Hugo include shortcodes, which splice another file into the page.
var includeRe = regexp.MustCompile(`\{\{[<%]-?\s*(?:include|readfile)\s+(?:file=)?"([^"]+)"[^}]*[%>]\}\}`)

// mergeIncludes adds headings, anchors and links from included files to pg.
func (s *Site) mergeIncludes(pg *model.Page, src []byte, contentAbs string, depth int) {
	if depth > 3 {
		return
	}
	for _, m := range includeRe.FindAllSubmatch(src, -1) {
		rel := strings.TrimPrefix(string(m[1]), "/")
		var b []byte
		var err error
		for _, base := range []string{contentAbs, s.Root} {
			cand := filepath.Join(base, filepath.FromSlash(rel))
			if filepath.Ext(cand) == "" {
				cand += ".md"
			}
			if b, err = os.ReadFile(cand); err == nil {
				break
			}
		}
		if err != nil {
			continue
		}
		inc := parse.MarkdownWith(b, s.mdOpts)
		for a := range inc.Anchors {
			pg.Anchors[a] = true
		}
		pg.Headings = append(pg.Headings, inc.Headings...)
		for _, l := range inc.Links {
			l.Line = 0 // the line is in the included file, not this one
			pg.Links = append(pg.Links, l)
		}
		pg.CodeBlocks = append(pg.CodeBlocks, inc.CodeBlocks...)
		pg.Words += inc.Words
		s.mergeIncludes(pg, b, contentAbs, depth+1)
	}
}
