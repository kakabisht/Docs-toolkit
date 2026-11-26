package crawl

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/model"
	"github.com/kakabisht/Docs-toolkit/internal/parse"
)

// LocalOptions configures LoadLocal.
type LocalOptions struct {
	ContentDir   string // relative to the repo root; auto-detected when empty
	StaticDirs   []string
	BaseURL      string
	Include      []string
	Exclude      []string
	MaxPages     int
	AnchorStyle  string   // github | mkdocs | sphinx | kramdown; detected from the generator when empty
	NavFile      string   // explicit navigation file, relative to the repo root
	MDXLinkProps []string // JSX props holding link targets
	NoGit        bool     // skip git history (last-modified dates)
}

var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".next": true, ".docusaurus": true, "build": true, "dist": true, "public": true, "resources": true, "_site": true, "site": true}

var pageExts = map[string]bool{".md": true, ".mdx": true, ".markdown": true, ".html": true, ".htm": true, ".adoc": true, ".asciidoc": true}

// LoadLocal reads a docs repo from disk. root may also be a single file: the
// repository around it is still read so its links resolve, and Only is set.
func LoadLocal(root string, opt LocalOptions) (*Site, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	var only string
	if !fi.IsDir() {
		only = root
		root = findRepoRoot(filepath.Dir(root))
	}
	s := &Site{Mode: Local, Source: root, Root: root, files: map[string]string{}, Duplicates: map[string][]string{}}
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
	s.Generator = detectGenerator(root)
	s.AnchorStyle = opt.AnchorStyle
	if s.AnchorStyle == "" {
		s.AnchorStyle = map[string]string{"mkdocs": parse.StyleMkDocs, "jekyll": parse.StyleKramdown, "sphinx": parse.StyleSphinx}[s.Generator]
		if s.AnchorStyle == "" {
			s.AnchorStyle = parse.StyleGitHub
		}
	}
	s.mdOpts = parse.Options{AnchorStyle: s.AnchorStyle, MDXLinkProps: opt.MDXLinkProps}
	if s.Generator == "hugo" {
		s.mdOpts.DefinitionTermIDs = hugoTermIDs(root)
	}
	s.Static = opt.StaticDirs
	if len(s.Static) == 0 {
		for _, d := range []string{"static", "public", "assets", "docs/public", "docs/static", "docs/assets", "."} {
			if fi, err := os.Stat(filepath.Join(root, d)); err == nil && fi.IsDir() {
				s.Static = append(s.Static, d)
			}
		}
	}
	contentAbs := filepath.Join(root, s.Content)
	if only != "" {
		if rel, err := filepath.Rel(contentAbs, only); err != nil || strings.HasPrefix(rel, "..") {
			// the file lives outside the detected content root: treat its folder as the content
			s.Content, _ = filepath.Rel(root, filepath.Dir(only))
			s.Content = filepath.ToSlash(s.Content)
			contentAbs = filepath.Dir(only)
		}
		rel, _ := filepath.Rel(root, only)
		s.Only = filepath.ToSlash(rel)
	}

	// every file in the repo, for asset and include resolution
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
		s.files[strings.ToLower(rel)] = rel
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
		rel, _ := filepath.Rel(contentAbs, p)
		rel = filepath.ToSlash(rel)
		if !pageExts[ext] || rel == "SUMMARY.md" { // mdBook/GitBook navigation, not a page
			return nil
		}
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
		pg, fm := s.parseFile(b, ext)
		pg.Path = rel
		pg.RepoPath = path.Join(s.Content, rel)
		pg.Source = "local"
		pg.Key = localRoute(rel, fm, s.Generator)
		if s.Base != nil {
			u := *s.Base
			u.Path = strings.TrimSuffix(s.Base.Path, "/") + pg.Key
			pg.URL = u.String()
		}
		s.resolveIncludes(pg, contentAbs, 0)
		s.Pages = append(s.Pages, pg)
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.Duplicates = dedupeKeys(s.Pages)
	var dates map[string]int64
	if !opt.NoGit {
		dates = gitDates(root)
	}
	owners := loadCodeowners(root)
	for _, p := range s.Pages {
		p.LastModified = dates[p.RepoPath]
		if p.Owner == "" {
			p.Owner = owners.owner(p.RepoPath)
		}
	}
	s.index()
	s.LLMSTxt = s.localLLMSTxt()
	s.loadNav(opt.NavFile)
	return s, nil
}

func (s *Site) parseFile(b []byte, ext string) (*model.Page, parse.FrontMatter) {
	var fm parse.FrontMatter
	switch ext {
	case ".html", ".htm":
		return parse.HTML(b), fm
	case ".adoc", ".asciidoc":
		pg := parse.AsciiDoc(b, s.mdOpts)
		return pg, parse.FrontMatter{Title: pg.Title, Aliases: pg.Aliases}
	}
	fm, _, _ = parse.SplitFrontMatter(b)
	return parse.MarkdownWith(b, s.mdOpts), fm
}

// findRepoRoot walks up from dir to the nearest folder that looks like the
// root of a docs repository: it has .git, a docsbot config or a site
// generator config. Without one it returns dir.
func findRepoRoot(dir string) string {
	markers := []string{".git", "docsbot.yml", "docsbot.yaml", "mkdocs.yml", "hugo.toml", "docusaurus.config.js", "docusaurus.config.ts", "conf.py", "antora.yml", "astro.config.mjs", "mint.json", "docs.json"}
	for d := dir; ; d = filepath.Dir(d) {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(d, m)); err == nil {
				return d
			}
		}
		if filepath.Dir(d) == d {
			return dir
		}
	}
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
	for _, c := range []string{"content/en", "content/docs", "content", "docs/content", "src/content/docs", "docs", "doc", "documentation", "pages", "src/pages", "modules/ROOT/pages"} {
		if fi, err := os.Stat(filepath.Join(root, c)); err == nil && fi.IsDir() {
			return c
		}
	}
	return "."
}

var numericPrefix = regexp.MustCompile(`^\d+[-_.]`)

// localRoute is the published path of a source file, honouring url and slug front matter.
func localRoute(rel string, fm parse.FrontMatter, generator string) string {
	if fm.URL != "" {
		return RouteKey(fm.URL)
	}
	dir := path.Dir(rel)
	if dir == "." {
		dir = ""
	}
	name := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	if generator == "docusaurus" {
		// Docusaurus drops number prefixes used for ordering, and honours slug and id
		var segs []string
		for _, sgm := range strings.Split(dir, "/") {
			segs = append(segs, numericPrefix.ReplaceAllString(sgm, ""))
		}
		dir = strings.Join(segs, "/")
		name = numericPrefix.ReplaceAllString(name, "")
		if strings.HasPrefix(fm.Slug, "/") {
			return RouteKey(fm.Slug)
		}
		if fm.ID != "" {
			name = fm.ID
		}
	}
	if IsIndexFile(rel) {
		return RouteKey("/" + dir)
	}
	if fm.Slug != "" {
		name = fm.Slug
	}
	return RouteKey("/" + path.Join(dir, name))
}

// dedupeKeys keeps keys unique when two files map to one route (foo.md and
// foo/index.md), and returns the clashes.
func dedupeKeys(pages []*model.Page) map[string][]string {
	sort.SliceStable(pages, func(i, j int) bool { return pages[i].Path < pages[j].Path })
	seen := map[string]int{}
	first := map[string]string{}
	dups := map[string][]string{}
	for _, p := range pages {
		k := p.Key
		if n := seen[k]; n > 0 {
			if len(dups[k]) == 0 {
				dups[k] = []string{first[k]}
			}
			dups[k] = append(dups[k], p.RepoPath)
			p.Key = fmt.Sprintf("%s~%d", k, n)
		} else {
			first[k] = p.RepoPath
		}
		seen[k]++
	}
	return dups
}

func keep(rel string, include, exclude []string) bool {
	if len(include) > 0 {
		ok := false
		for _, g := range include {
			if Match(g, rel) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, g := range exclude {
		if Match(g, rel) {
			return false
		}
	}
	return true
}

// Match supports path.Match globs plus "**/" prefixes and "/**" suffixes.
func Match(glob, rel string) bool {
	if strings.HasSuffix(glob, "/**") {
		prefix := strings.TrimSuffix(glob, "/**")
		if !strings.ContainsAny(prefix, "*?[") {
			return rel == prefix || strings.HasPrefix(rel, prefix+"/")
		}
	}
	if strings.HasPrefix(glob, "**/") {
		g := strings.TrimPrefix(glob, "**/")
		parts := strings.Split(rel, "/")
		for i := range parts {
			if Match(g, strings.Join(parts[i:], "/")) {
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
		actual, ok := s.files[strings.ToLower(rel)]
		if !ok {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Root, actual))
		if err != nil {
			continue
		}
		return model.LLMSTxt{Location: actual, Found: true, Size: len(b), Links: parse.LLMSTxtLinks(b)}
	}
	return model.LLMSTxt{Location: "the content root or static folders"}
}

// detectGenerator recognises static site generators whose link rules differ.
func detectGenerator(root string) string {
	checks := []struct {
		gen   string
		files []string
	}{
		{"hugo", []string{"hugo.toml", "hugo.yaml", "hugo.yml", "hugo.json", "config/_default/hugo.toml"}},
		{"docusaurus", []string{"docusaurus.config.js", "docusaurus.config.ts", "docusaurus.config.mjs"}},
		{"mkdocs", []string{"mkdocs.yml", "mkdocs.yaml"}},
		{"sphinx", []string{"conf.py", "docs/conf.py", "source/conf.py"}},
		{"antora", []string{"antora.yml", "antora-playbook.yml"}},
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

// resolveIncludes finds each included file, then adds its headings, anchors
// and links to pg. Links from an include keep the include's own file and
// line (Link.From), so a broken link in a partial is reported once, where it
// is written, however many pages include it.
func (s *Site) resolveIncludes(pg *model.Page, contentAbs string, depth int) {
	if depth > 3 {
		return
	}
	incs := pg.Includes
	var nested []model.Include
	for i := range incs {
		inc := &incs[i]
		if depth > 0 && inc.Path != "" {
			continue
		}
		target := strings.TrimPrefix(inc.Target, "/")
		var cands []string
		dir := path.Dir(pg.RepoPath)
		if strings.HasPrefix(inc.Target, "@site/") {
			cands = append(cands, strings.TrimPrefix(inc.Target, "@site/"))
		}
		if !strings.HasPrefix(inc.Target, "/") {
			cands = append(cands, path.Join(dir, target))
		}
		cands = append(cands, path.Join(s.Content, target), target)
		for _, c := range cands {
			for _, ext := range []string{"", ".md", ".mdx", ".adoc"} {
				if actual, ok := s.files[strings.ToLower(path.Clean(c+ext))]; ok {
					inc.Path = actual
					break
				}
			}
			if inc.Path != "" {
				break
			}
		}
		if inc.Path == "" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(inc.Path)))
		if err != nil {
			continue
		}
		sub, _ := s.parseFile(b, strings.ToLower(path.Ext(inc.Path)))
		for a := range sub.Anchors {
			pg.Anchors[a] = true
		}
		// headings stay with the partial: their position in the page is unknown
		for _, l := range sub.Links {
			if l.From == "" {
				l.From = inc.Path
				l.FromModule = inc.Kind == "import"
			}
			pg.Links = append(pg.Links, l)
		}
		pg.CodeBlocks = append(pg.CodeBlocks, sub.CodeBlocks...)
		pg.InlineCode = append(pg.InlineCode, sub.InlineCode...)
		pg.Paragraphs = append(pg.Paragraphs, sub.Paragraphs...)
		pg.Words += sub.Words
		// nested includes resolve relative to the partial
		subPage := &model.Page{RepoPath: inc.Path, Anchors: pg.Anchors, Includes: sub.Includes}
		s.resolveIncludes(subPage, contentAbs, depth+1)
		pg.Links = append(pg.Links, subPage.Links...)
		for _, si := range subPage.Includes {
			si.Kind += " (nested)"
			nested = append(nested, si)
		}
	}
	pg.Includes = append(incs, nested...)
}
