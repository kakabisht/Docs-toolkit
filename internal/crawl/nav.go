package crawl

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/docsbot/docsbot/internal/model"
)

// Nav is the navigation tree of a local docs set.
type Nav struct {
	Source   string      `json:"source"`   // file the nav came from, or "directory"
	Explicit bool        `json:"explicit"` // false when inferred from folders
	Entries  []*NavEntry `json:"entries"`
	inNav    map[string]bool
}

// NavEntry is one navigation item.
type NavEntry struct {
	Title    string      `json:"title"`
	Target   string      `json:"target,omitempty"` // as written
	Key      string      `json:"key,omitempty"`    // resolved page key
	File     string      `json:"file"`             // file that defines the entry (repository path)
	Line     int         `json:"line,omitempty"`
	Missing  bool        `json:"missing,omitempty"` // target page does not exist
	External bool        `json:"external,omitempty"`
	Children []*NavEntry `json:"children,omitempty"`
}

// Contains reports whether the page with key k appears in the navigation.
func (n *Nav) Contains(k string) bool { return n != nil && n.inNav[k] }

// Walk visits every entry depth-first with its depth (1 = top level).
func (n *Nav) Walk(f func(e *NavEntry, depth int, parent *NavEntry)) {
	var rec func([]*NavEntry, int, *NavEntry)
	rec = func(es []*NavEntry, d int, parent *NavEntry) {
		for _, e := range es {
			f(e, d, parent)
			rec(e.Children, d+1, e)
		}
	}
	if n != nil {
		rec(n.Entries, 1, nil)
	}
}

// loadNav finds a navigation source: an explicit nav file, mkdocs.yml,
// SUMMARY.md or Docusaurus sidebars.json. Without one it builds the tree from
// folders and index pages.
func (s *Site) loadNav(navFile string) {
	cands := []string{}
	if navFile != "" {
		cands = append(cands, navFile)
	}
	cands = append(cands, "mkdocs.yml", "mkdocs.yaml", path.Join(s.Content, "SUMMARY.md"), "SUMMARY.md", "sidebars.json")
	for _, c := range cands {
		b, err := os.ReadFile(filepath.Join(s.Root, filepath.FromSlash(c)))
		if err != nil {
			continue
		}
		var nav *Nav
		switch {
		case strings.HasPrefix(path.Base(c), "mkdocs."):
			nav = s.mkdocsNav(c, b)
		case strings.HasSuffix(c, ".md"):
			nav = s.summaryNav(c, b)
		case strings.HasSuffix(c, ".json") || strings.HasSuffix(c, ".yml") || strings.HasSuffix(c, ".yaml"):
			nav = s.genericNav(c, b)
		}
		if nav != nil && len(nav.Entries) > 0 {
			s.Nav = nav
			break
		}
	}
	if s.Nav == nil {
		s.Nav = s.directoryNav()
	}
	s.Nav.inNav = map[string]bool{}
	s.Nav.Walk(func(e *NavEntry, _ int, _ *NavEntry) {
		if e.Key != "" && !e.Missing {
			s.Nav.inNav[e.Key] = true
		}
	})
}

func (s *Site) resolveNavTarget(e *NavEntry, from *model.Page) {
	if e.Target == "" {
		return
	}
	t := s.Resolve(from, e.Target)
	switch t.Kind {
	case KindExternal, KindOther:
		e.External = true
	default:
		if t.Page != nil {
			e.Key = t.Page.Key
			if e.Title == "" {
				e.Title = t.Page.Title
			}
		} else if t.Kind != KindAsset || !t.Exists {
			e.Missing = true
		}
	}
}

// mkdocsNav reads the nav: key of mkdocs.yml, keeping line numbers.
func (s *Site) mkdocsNav(file string, b []byte) *Nav {
	var doc yaml.Node
	if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	var navNode *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "nav" {
			navNode = root.Content[i+1]
		}
	}
	if navNode == nil {
		return nil
	}
	from := &model.Page{Key: "/", Path: "_nav"} // nav paths are relative to docs_dir
	var items func(n *yaml.Node) []*NavEntry
	items = func(n *yaml.Node) []*NavEntry {
		var out []*NavEntry
		for _, it := range n.Content {
			switch it.Kind {
			case yaml.ScalarNode:
				e := &NavEntry{Target: it.Value, File: file, Line: it.Line}
				s.resolveNavTarget(e, from)
				out = append(out, e)
			case yaml.MappingNode:
				for i := 0; i+1 < len(it.Content); i += 2 {
					k, v := it.Content[i], it.Content[i+1]
					e := &NavEntry{Title: k.Value, File: file, Line: k.Line}
					if v.Kind == yaml.SequenceNode {
						e.Children = items(v)
					} else {
						e.Target = v.Value
						s.resolveNavTarget(e, from)
					}
					out = append(out, e)
				}
			}
		}
		return out
	}
	return &Nav{Source: file, Explicit: true, Entries: items(navNode)}
}

var summaryItem = regexp.MustCompile(`^(\s*)[-*+]\s+(?:\[([^\]]*)\]\(([^)]*)\)|(.+))\s*$`)

// summaryNav reads an mdBook/GitBook style SUMMARY.md: nested lists of links.
func (s *Site) summaryNav(file string, b []byte) *Nav {
	rel := strings.TrimPrefix(strings.TrimPrefix(file, s.Content), "/")
	from := &model.Page{Key: "/", Path: rel}
	nav := &Nav{Source: file, Explicit: true}
	type lvl struct {
		indent int
		e      *NavEntry
	}
	var stack []lvl
	for i, line := range strings.Split(string(b), "\n") {
		m := summaryItem.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		e := &NavEntry{Title: strings.TrimSpace(m[2] + m[4]), Target: m[3], File: file, Line: i + 1}
		s.resolveNavTarget(e, from)
		ind := len(strings.ReplaceAll(m[1], "\t", "    "))
		for len(stack) > 0 && stack[len(stack)-1].indent >= ind {
			stack = stack[:len(stack)-1]
		}
		if len(stack) == 0 {
			nav.Entries = append(nav.Entries, e)
		} else {
			p := stack[len(stack)-1].e
			p.Children = append(p.Children, e)
		}
		stack = append(stack, lvl{ind, e})
	}
	return nav
}

// genericNav reads a YAML or JSON list of {title, path|url|id, children|items}
// entries, which covers Docusaurus sidebars.json and hand-written nav files.
func (s *Site) genericNav(file string, b []byte) *Nav {
	var doc yaml.Node // YAML is a superset of JSON
	if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 {
		return nil
	}
	from := &model.Page{Key: "/", Path: "_nav"}
	var items func(n *yaml.Node) []*NavEntry
	items = func(n *yaml.Node) []*NavEntry {
		var out []*NavEntry
		switch n.Kind {
		case yaml.MappingNode: // Docusaurus: {sidebarName: [...]} or {Category: [...]}
			for i := 0; i+1 < len(n.Content); i += 2 {
				e := &NavEntry{Title: n.Content[i].Value, File: file, Line: n.Content[i].Line, Children: items(n.Content[i+1])}
				out = append(out, e)
			}
		case yaml.SequenceNode:
			for _, it := range n.Content {
				switch it.Kind {
				case yaml.ScalarNode: // bare doc id or path
					e := &NavEntry{Target: it.Value, File: file, Line: it.Line}
					s.resolveNavTarget(e, from)
					out = append(out, e)
				case yaml.MappingNode:
					e := &NavEntry{File: file, Line: it.Line}
					isMap := true
					for i := 0; i+1 < len(it.Content); i += 2 {
						k, v := it.Content[i].Value, it.Content[i+1]
						switch k {
						case "title", "label", "name":
							e.Title = v.Value
							isMap = false
						case "path", "url", "href", "id", "file", "page", "link":
							if v.Kind == yaml.ScalarNode {
								e.Target = v.Value
							} else if v.Kind == yaml.MappingNode { // Docusaurus link: {type: doc, id: x}
								for j := 0; j+1 < len(v.Content); j += 2 {
									if v.Content[j].Value == "id" {
										e.Target = v.Content[j+1].Value
									}
								}
							}
							isMap = false
						case "children", "items", "pages", "sections":
							e.Children = items(v)
							isMap = false
						case "type", "collapsed", "collapsible", "className":
							isMap = false
						}
					}
					if isMap { // {Title: path} shorthand
						for i := 0; i+1 < len(it.Content); i += 2 {
							c := &NavEntry{Title: it.Content[i].Value, File: file, Line: it.Content[i].Line}
							if it.Content[i+1].Kind == yaml.ScalarNode {
								c.Target = it.Content[i+1].Value
								s.resolveNavTarget(c, from)
							} else {
								c.Children = items(it.Content[i+1])
							}
							out = append(out, c)
						}
						continue
					}
					s.resolveNavTarget(e, from)
					out = append(out, e)
				}
			}
		}
		return out
	}
	return &Nav{Source: file, Explicit: true, Entries: items(doc.Content[0])}
}

// directoryNav builds the tree from folders: each folder's index page is the
// parent of the pages and subfolders inside it.
func (s *Site) directoryNav() *Nav {
	nav := &Nav{Source: "directory"}
	byDir := map[string]*NavEntry{}
	var dirs []string
	for _, p := range s.Pages {
		if p.Path == "" {
			continue
		}
		d := path.Dir(p.Path)
		if IsIndexFile(p.Path) {
			byDir[d] = &NavEntry{Title: p.Title, Key: p.Key, File: p.RepoPath}
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	parentOf := func(d string) *NavEntry {
		for d != "." && d != "/" && d != "" {
			d = path.Dir(d)
			if e, ok := byDir[d]; ok {
				return e
			}
		}
		return nil
	}
	attach := func(e *NavEntry, parent *NavEntry) {
		if parent == nil {
			nav.Entries = append(nav.Entries, e)
		} else {
			parent.Children = append(parent.Children, e)
		}
	}
	for _, d := range dirs {
		if d == "." {
			attach(byDir[d], nil)
			continue
		}
		attach(byDir[d], parentOf(d))
	}
	for _, p := range s.Pages {
		if p.Path == "" || IsIndexFile(p.Path) {
			continue
		}
		e := &NavEntry{Title: p.Title, Key: p.Key, File: p.RepoPath}
		if parent, ok := byDir[path.Dir(p.Path)]; ok {
			attach(e, parent)
		}
		// pages in folders without an index get no listing in most generators
	}
	return nav
}
