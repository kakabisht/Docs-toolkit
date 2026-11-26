// Package agentready generates the files that help AI agents use a docs
// site: llms.txt, llms-full.txt, a Markdown copy of every page, a "copy page
// as Markdown" script and a discovery snippet. It writes to an output folder
// and never changes the docs source; publishing is left to the site's build.
package agentready

import (
	_ "embed"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/docsbot/docsbot/internal/crawl"
	"github.com/docsbot/docsbot/internal/model"
)

// Options configures Generate.
type Options struct {
	Out       string // output folder; must be outside the content root
	Title     string // llms.txt title (default: the home page title)
	Summary   string // llms.txt summary (default: the home page description)
	Directive bool   // start each Markdown page with a one-line pointer to llms.txt
	MaxChars  int    // split llms.txt into section files above this size (default 50,000)
	DryRun    bool
}

// Result lists what was (or would be) written and what needs a person.
type Result struct {
	Out        string   `json:"out"`
	Files      []string `json:"files"`
	Pages      int      `json:"pages"`
	InLLMSTxt  int      `json:"in_llms_txt"`
	Optional   []string `json:"optional,omitempty"`            // pages not in the navigation, listed under Optional
	Skipped    []Note   `json:"skipped,omitempty"`             // pages with no Markdown output
	NoDesc     []string `json:"without_description,omitempty"` // llms.txt entries without a description
	Rewritten  int      `json:"links_rewritten"`               // internal links pointed at the .md copies
	Inlined    int      `json:"includes_inlined"`
	KeptMarkup []Note   `json:"kept_markup,omitempty"` // shortcodes or JSX left as written
	Warnings   []string `json:"warnings,omitempty"`
	Split      bool     `json:"split"` // llms.txt is an index of section files
}

// Note is a page with a reason.
type Note struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

//go:embed copy-as-markdown.js
var copyJS string

// MarkdownPath is where a page's Markdown copy goes, relative to the site
// root: /guide/install/ -> guide/install.md, / -> index.md. It matches the
// URLs agents and afdocs try first.
func MarkdownPath(route string) string {
	r := strings.Trim(route, "/")
	if r == "" {
		return "index.md"
	}
	return r + ".md"
}

type entry struct {
	p       *model.Page
	section string
}

// Generate writes the files for a local docs set.
func Generate(s *crawl.Site, opt Options) (*Result, error) {
	if s.Mode != crawl.Local {
		return nil, fmt.Errorf("generate needs a local docs folder")
	}
	if opt.Out == "" {
		opt.Out = filepath.Join("build", "agent-ready")
	}
	if s.Content != "" {
		outAbs, _ := filepath.Abs(opt.Out)
		d, _ := filepath.Abs(s.Content)
		if outAbs == d || strings.HasPrefix(outAbs, d+string(filepath.Separator)) {
			return nil, fmt.Errorf("output folder %s is inside the content root %s: generated pages would become docs pages; choose another --out", opt.Out, s.Content)
		}
	}
	res := &Result{Out: opt.Out}
	files := map[string]string{}
	home := s.Page("/")
	title, summary := opt.Title, opt.Summary
	if title == "" && home != nil {
		title = home.Title
	}
	if title == "" {
		title = "Documentation"
		res.Warnings = append(res.Warnings, "No home page title: llms.txt uses the placeholder title \"Documentation\"; pass --title.")
	}
	if summary == "" && home != nil {
		summary = home.Description
	}
	if summary == "" {
		res.Warnings = append(res.Warnings, "No summary: add a description to the home page or pass --summary.")
	}
	if s.Base == nil {
		res.Warnings = append(res.Warnings, "No base_url: llms.txt links are root-relative. Set base_url (or --base-url) for absolute links.")
	}
	// order: navigation first, then pages outside it under Optional
	order, sections := navOrder(s)
	listed := map[string]bool{}
	for _, e := range order {
		listed[e.p.Key] = true
	}
	var rest []*model.Page
	for _, p := range s.Pages {
		if p.Err != "" || p.Draft || listed[p.Key] {
			continue
		}
		rest = append(rest, p)
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].Key < rest[j].Key })
	for _, p := range rest {
		order = append(order, entry{p, "Optional"})
		res.Optional = append(res.Optional, p.Key)
	}
	if !containsStr(sections, "Optional") && len(rest) > 0 {
		sections = append(sections, "Optional")
	}

	// per-page Markdown
	var full strings.Builder
	written := map[string]bool{}
	for _, e := range order {
		p := e.p
		if written[p.Key] {
			continue
		}
		written[p.Key] = true
		if p.Draft {
			res.Skipped = append(res.Skipped, Note{p.RepoPath, "draft"})
			continue
		}
		if p.Format != "markdown" || strings.HasSuffix(p.Path, ".adoc") {
			res.Skipped = append(res.Skipped, Note{p.RepoPath, "not Markdown: convert it first (docsbot convert)"})
			continue
		}
		md, kept := pageMarkdown(s, p, opt, res)
		if kept != "" {
			res.KeptMarkup = append(res.KeptMarkup, Note{p.RepoPath, kept})
		}
		if opt.Directive {
			files[MarkdownPath(p.Key)] = fmt.Sprintf("> For AI agents: the documentation index is at %s.\n\n", link(s, "llms.txt")) + md
		} else {
			files[MarkdownPath(p.Key)] = md
		}
		res.Pages++
		fmt.Fprintf(&full, "<!-- source: %s -->\n\n%s\n\n---\n\n", pageURL(s, p), strings.TrimSpace(md))
	}

	// llms.txt: one file, or an index of section files when one file would
	// be too big for agents to read whole
	if opt.MaxChars == 0 {
		opt.MaxChars = 50000
	}
	header := func(b *strings.Builder, t, sum string) {
		fmt.Fprintf(b, "# %s\n\n", t)
		if sum != "" {
			fmt.Fprintf(b, "> %s\n\n", oneLine(sum))
		}
	}
	secLines := map[string][]string{}
	for _, e := range order {
		if files[MarkdownPath(e.p.Key)] == "" {
			continue
		}
		line := fmt.Sprintf("- [%s](%s)", firstNonEmpty(e.p.Title, e.p.Key), link(s, MarkdownPath(e.p.Key)))
		if d := oneLine(e.p.Description); d != "" {
			line += ": " + d
		} else {
			res.NoDesc = append(res.NoDesc, e.p.RepoPath)
		}
		secLines[e.section] = append(secLines[e.section], line)
		res.InLLMSTxt++
	}
	var lt strings.Builder
	header(&lt, title, summary)
	fmt.Fprintf(&lt, "Every page below is Markdown. The whole set is in one file at %s.\n", link(s, "llms-full.txt"))
	for _, sec := range sections {
		if len(secLines[sec]) > 0 {
			fmt.Fprintf(&lt, "\n## %s\n\n%s\n", sec, strings.Join(secLines[sec], "\n"))
		}
	}
	if lt.Len() <= opt.MaxChars {
		files["llms.txt"] = lt.String()
	} else {
		res.Split = true
		var idx strings.Builder
		header(&idx, title, summary)
		fmt.Fprintf(&idx, "The index is split by section so each file stays readable in one go. Every page is Markdown; the whole set is in one file at %s.\n\n## Sections\n\n", link(s, "llms-full.txt"))
		used := map[string]bool{}
		for _, sec := range sections {
			if len(secLines[sec]) == 0 {
				continue
			}
			slug := slugify(sec)
			for i := 2; used[slug]; i++ {
				slug = fmt.Sprintf("%s-%d", slugify(sec), i)
			}
			used[slug] = true
			rel := "llms/" + slug + ".txt"
			var b strings.Builder
			header(&b, title+": "+sec, "")
			fmt.Fprintf(&b, "Part of %s.\n\n%s\n", link(s, "llms.txt"), strings.Join(secLines[sec], "\n"))
			files[rel] = b.String()
			fmt.Fprintf(&idx, "- [%s](%s): %d pages\n", sec, link(s, rel), len(secLines[sec]))
			if b.Len() > opt.MaxChars {
				res.Warnings = append(res.Warnings, fmt.Sprintf("Section %q alone is %d characters; split it in the navigation, or shorten descriptions.", sec, b.Len()))
			}
		}
		files["llms.txt"] = idx.String()
	}
	files["llms-full.txt"] = full.String()
	files["copy-as-markdown.js"] = copyJS
	files["discovery-snippet.html"] = discoverySnippet(s)
	files["README.md"] = readme(s)

	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	res.Files = names
	if opt.DryRun {
		return res, nil
	}
	for _, n := range names {
		p := filepath.Join(opt.Out, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(p, []byte(files[n]), 0o644); err != nil {
			return nil, err
		}
	}
	return res, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "section"
	}
	return s
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// navOrder lists pages in navigation order with their llms.txt section:
// the top-level entry they sit under, or "Docs" for top-level pages.
func navOrder(s *crawl.Site) ([]entry, []string) {
	var out []entry
	var sections []string
	seen := map[string]bool{}
	add := func(key, sec string) {
		p := s.Page(key)
		if p == nil || p.Err != "" || p.Draft || seen[key] {
			return
		}
		seen[key] = true
		if !containsStr(sections, sec) {
			sections = append(sections, sec)
		}
		out = append(out, entry{p, sec})
	}
	if s.Nav == nil {
		return nil, nil
	}
	var walk func(e *crawl.NavEntry, sec string)
	walk = func(e *crawl.NavEntry, sec string) {
		if e.Key != "" {
			add(e.Key, sec)
		}
		for _, c := range e.Children {
			walk(c, sec)
		}
	}
	tops := s.Nav.Entries
	// a folder-derived navigation has one root (the home page) holding
	// everything: its children are the real top level
	for len(tops) == 1 && len(tops[0].Children) > 0 {
		if tops[0].Key != "" {
			add(tops[0].Key, "Docs")
		}
		tops = tops[0].Children
	}
	for _, top := range tops {
		if len(top.Children) == 0 {
			walk(top, "Docs")
			continue
		}
		walk(top, firstNonEmpty(top.Title, strings.Trim(top.Key, "/")))
	}
	return out, sections
}

func pageURL(s *crawl.Site, p *model.Page) string {
	if p.URL != "" {
		return p.URL
	}
	return p.Key
}

func link(s *crawl.Site, rel string) string {
	if s.Base != nil {
		u := *s.Base
		u.Path = path.Join(u.Path, rel)
		return u.String()
	}
	return "/" + rel
}

var (
	shortcodeRe = regexp.MustCompile(`\{\{[<%].*?[%>]\}\}`)
	jsxRe       = regexp.MustCompile(`(?m)^\s*<[A-Z][A-Za-z0-9.]*[\s/>]`)
	h1Re        = regexp.MustCompile(`(?m)^#\s+\S`)
)

// pageMarkdown builds the Markdown copy of one page: front matter removed,
// includes inlined, internal links pointed at the .md copies, a title
// heading when the body has none.
func pageMarkdown(s *crawl.Site, p *model.Page, opt Options, res *Result) (string, string) {
	lines := strings.Split(strings.ReplaceAll(string(p.Raw), "\r\n", "\n"), "\n")
	body := lines
	if p.FrontMatterEnd > 0 && p.FrontMatterEnd <= len(lines) {
		body = lines[p.FrontMatterEnd:]
	}
	offset := len(lines) - len(body) // file line n is body[n-1-offset]
	// links: rewrite on their own line, so the rest of the text stays byte for byte
	for _, l := range p.Links {
		if l.From != "" || l.Line <= offset || l.Line-1-offset >= len(body) || l.Image {
			continue
		}
		t := s.Resolve(p, l.Href)
		if t.Page == nil || (t.Kind != crawl.KindInternal) {
			continue
		}
		target := link(s, MarkdownPath(t.Page.Key))
		if t.Fragment != "" {
			target += "#" + t.Fragment
		}
		i := l.Line - 1 - offset
		for _, pre := range []string{"](" + l.Href + ")", "](" + l.Href + " ", "](<" + l.Href + ">)"} {
			if strings.Contains(body[i], pre) {
				body[i] = strings.Replace(body[i], pre, strings.Replace(pre, l.Href, target, 1), 1)
				res.Rewritten++
				break
			}
		}
	}
	// includes: inline the partial, replacing the include line
	for _, inc := range p.Includes {
		if inc.Path == "" || inc.Line <= offset || inc.Line-1-offset >= len(body) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(inc.Path)))
		if err != nil {
			continue
		}
		text := strings.TrimRight(stripFrontMatter(string(b)), "\n")
		i := inc.Line - 1 - offset
		indent := body[i][:len(body[i])-len(strings.TrimLeft(body[i], " \t"))]
		body[i] = indent + strings.ReplaceAll(text, "\n", "\n"+indent)
		res.Inlined++
	}
	md := strings.TrimLeft(strings.Join(body, "\n"), "\n")
	if !h1Re.MatchString(md) && p.Title != "" {
		md = "# " + p.Title + "\n\n" + md
	}
	var kept []string
	if shortcodeRe.MatchString(md) {
		kept = append(kept, "Hugo shortcodes")
	}
	if strings.HasSuffix(p.Path, ".mdx") && jsxRe.MatchString(md) {
		kept = append(kept, "JSX components")
	}
	if !strings.HasSuffix(md, "\n") {
		md += "\n"
	}
	return md, strings.Join(kept, " and ")
}

func stripFrontMatter(s string) string {
	for _, fence := range []string{"---", "+++"} {
		if strings.HasPrefix(s, fence+"\n") {
			if i := strings.Index(s[len(fence)+1:], "\n"+fence); i >= 0 {
				rest := s[len(fence)+1+i+len(fence)+1:]
				return strings.TrimLeft(rest, "\n")
			}
		}
	}
	return s
}

func discoverySnippet(s *crawl.Site) string {
	return fmt.Sprintf(`<!--
  Discovery snippet from docsbot agent-ready generate. Add it to your site's
  page template; replace {{PAGE_MD_URL}} with each page's Markdown URL
  (the page URL with .md added; the home page is /index.md).
-->

<!-- 1. In <head>: the Markdown version of this page -->
<link rel="alternate" type="text/markdown" href="{{PAGE_MD_URL}}">

<!-- 2. Near the top of <body>, outside <nav>: a visible pointer to llms.txt.
     afdocs looks for a link to /llms.txt in the page body. -->
<p class="agent-note">For AI agents: the documentation index is at
  <a href="%s">llms.txt</a>, and every page is available as Markdown.</p>

<!-- 3. Optional: a "Copy page as Markdown" button -->
<script src="/copy-as-markdown.js" defer></script>
`, link(s, "llms.txt"))
}

func readme(s *crawl.Site) string {
	gen := s.Generator
	where := "the folder your site serves at its root (its static or public folder)"
	switch gen {
	case "mkdocs":
		where = "a folder listed in mkdocs.yml so it is copied as is (for example docs/ with `not_in_nav`, or use a plugin such as mkdocs-llmstxt)"
	case "hugo":
		where = "static/ (Hugo copies it to the site root)"
	case "docusaurus":
		where = "static/ (Docusaurus copies it to the site root)"
	}
	return fmt.Sprintf(`# Agent-ready files

Generated by `+"`docsbot agent-ready generate`"+`. Nothing here has been published,
and the docs source was not changed.

| File | What it is |
| --- | --- |
| llms.txt | Index of the docs for AI agents (https://llmstxt.org), from the navigation and page descriptions |
| llms-full.txt | Every page in one Markdown file |
| *.md | A Markdown copy of each page, at its URL with .md added |
| copy-as-markdown.js | Adds a "Copy page as Markdown" button that fetches the page's .md copy |
| discovery-snippet.html | Template additions so agents find llms.txt and the Markdown copies |

To publish, copy these files into %s as part of the site build, add the
discovery snippet to the page template, then run `+"`docsbot agent-ready check`"+`
against the deployed site.
`, where)
}
