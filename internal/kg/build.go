package kg

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/crawl"
	"github.com/kakabisht/Docs-toolkit/internal/doctype"
	"github.com/kakabisht/Docs-toolkit/internal/graph"
	"github.com/kakabisht/Docs-toolkit/internal/model"
	"github.com/kakabisht/Docs-toolkit/internal/vocab"
)

// Options configures Build.
type Options struct {
	Vocab            *vocab.Vocab
	ContentTypes     doctype.Overrides
	Commands         []string // CLI names that are product commands; detected when empty
	NoSimilarity     bool
	RelatedThreshold float64          // cosine similarity for related-to (default 0.35)
	OverlapThreshold float64          // shingle overlap for overlaps-with (default 0.5)
	MaxRelated       int              // related-to edges per page (default 3)
	Repo             string           // repository name
	IgnoreHrefs      []*regexp.Regexp // raw hrefs that are not links (render-hook shorthands)
	Extractor        Extractor
}

// Extractor is an optional model-backed extractor. Its edges are labelled
// inferred, must cite a span from the page, and never replace explicit edges.
type Extractor interface {
	Extract(p *model.Page) ([]Edge, []*Node, error)
}

// Build creates the knowledge graph of a local docs set.
func Build(s *crawl.Site, opt Options) *Graph {
	if opt.RelatedThreshold == 0 {
		opt.RelatedThreshold = 0.35
	}
	if opt.OverlapThreshold == 0 {
		opt.OverlapThreshold = 0.5
	}
	if opt.MaxRelated == 0 {
		opt.MaxRelated = 3
	}
	b := &builder{s: s, opt: opt, g: &Graph{Source: s.Source, Meta: map[string]string{}}, seen: map[string]bool{}}
	b.pg = graph.Build(s)
	b.g.Entry = PageID(b.pg.Entry)
	b.g.Meta["generator"] = s.Generator
	b.g.Meta["anchor_style"] = s.AnchorStyle
	if s.Nav != nil {
		b.g.Meta["nav"] = s.Nav.Source
	}
	b.pages()
	b.links()
	b.nav()
	b.metadata()
	b.entities()
	if !opt.NoSimilarity {
		b.similarity()
	}
	if opt.Extractor != nil {
		b.modelExtraction()
	}
	b.g.Normalize()
	return b.g
}

type builder struct {
	s    *crawl.Site
	opt  Options
	g    *Graph
	pg   *graph.Graph
	seen map[string]bool // edge dedupe: from|type|to
}

func fileOf(p *model.Page) string {
	if p.RepoPath != "" {
		return p.RepoPath
	}
	if p.FinalURL != "" {
		return p.FinalURL
	}
	return p.URL
}

// edge adds e once per (from, type, to), keeping the first source.
func (b *builder) edge(e Edge) {
	k := e.From + "|" + e.Type + "|" + e.To
	if b.seen[k] {
		return
	}
	b.seen[k] = true
	b.g.Edges = append(b.g.Edges, e)
}

func (b *builder) pages() {
	for _, p := range b.s.Pages {
		if p.Err != "" {
			continue
		}
		n := b.pg.Node(p.Key)
		props := map[string]string{"path": fileOf(p), "words": fmt.Sprint(p.Words)}
		if p.URL != "" {
			props["url"] = p.URL
		}
		if p.Description != "" {
			props["description"] = p.Description
		}
		if n != nil {
			props["rank"] = fmt.Sprintf("%.4f", n.Rank)
			props["depth"] = fmt.Sprint(n.Depth)
		}
		if p.LastModified > 0 {
			props["last_modified"] = fmt.Sprint(p.LastModified)
		}
		if b.s.Nav.Contains(p.Key) {
			props["in_nav"] = "true"
		}
		label := p.Title
		if label == "" {
			label = p.Key
		}
		b.g.add(&Node{ID: PageID(p.Key), Type: TPage, Label: label, Props: props})
		// sections and the heading hierarchy
		var stack []model.Heading
		for _, h := range p.Headings {
			if h.Anchor == "" || h.Level == 1 {
				continue
			}
			id := SectionID(p.Key, h.Anchor)
			b.g.add(&Node{ID: id, Type: TSection, Label: h.Text, Props: map[string]string{"page": p.Key, "level": fmt.Sprint(h.Level)}})
			for len(stack) > 0 && stack[len(stack)-1].Level >= h.Level {
				stack = stack[:len(stack)-1]
			}
			parent := PageID(p.Key)
			if len(stack) > 0 {
				parent = SectionID(p.Key, stack[len(stack)-1].Anchor)
			}
			b.edge(Edge{From: id, To: parent, Type: EChildOf, Derivation: Explicit, File: fileOf(p), Line: h.Line, Span: h.Text})
			stack = append(stack, h)
		}
	}
}

func (b *builder) links() {
	s := b.s
	for _, p := range s.Pages {
		if p.Err != "" {
			continue
		}
		from := PageID(p.Key)
		for _, l := range p.Links {
			if l.Nav || l.Image || ignored(b.opt.IgnoreHrefs, l.Href) {
				continue
			}
			file := fileOf(p)
			if l.From != "" {
				file = l.From
			}
			t := s.Resolve(p, l.Href)
			switch t.Kind {
			case crawl.KindInternal, crawl.KindAnchor:
			default:
				continue
			}
			if !t.Exists || t.Page == nil {
				id := MissingID(l.Href)
				b.g.add(&Node{ID: id, Type: TMissing, Label: l.Href})
				b.g.Edges = append(b.g.Edges, Edge{From: from, To: id, Type: ELinksTo, Derivation: Explicit, File: file, Line: l.Line, Span: l.Text, Broken: true})
				continue
			}
			to := PageID(t.Page.Key)
			if t.Fragment != "" {
				if hasAnchor(t.Page.Anchors, t.Fragment) {
					if b.g.Node(SectionID(t.Page.Key, t.Fragment)) != nil {
						to = SectionID(t.Page.Key, t.Fragment)
					}
				} else {
					id := MissingID(t.Page.Key + "#" + t.Fragment)
					b.g.add(&Node{ID: id, Type: TMissing, Label: t.Page.Key + "#" + t.Fragment})
					b.g.Edges = append(b.g.Edges, Edge{From: from, To: id, Type: ELinksTo, Derivation: Explicit, File: file, Line: l.Line, Span: l.Text, Broken: true})
					continue
				}
			}
			if to == from {
				continue
			}
			b.g.Edges = append(b.g.Edges, Edge{From: from, To: to, Type: ELinksTo, Derivation: Explicit, File: file, Line: l.Line, Span: l.Text})
		}
		for _, inc := range p.Includes {
			if inc.Path == "" {
				continue
			}
			id := PartialID(inc.Path)
			b.g.add(&Node{ID: id, Type: TPartial, Label: inc.Path})
			b.edge(Edge{From: from, To: id, Type: EIncludes, Derivation: Explicit, File: fileOf(p), Line: inc.Line, Span: inc.Target})
		}
	}
}

func ignored(res []*regexp.Regexp, href string) bool {
	for _, re := range res {
		if re.MatchString(href) {
			return true
		}
	}
	return false
}

func hasAnchor(anchors map[string]bool, frag string) bool {
	if anchors[frag] {
		return true
	}
	f, err := url.PathUnescape(frag)
	return err == nil && anchors[f]
}

func (b *builder) nav() {
	nav := b.s.Nav
	if nav == nil {
		return
	}
	nav.Walk(func(e *crawl.NavEntry, _ int, parent *crawl.NavEntry) {
		if parent == nil || e.Key == "" || e.Missing {
			return
		}
		// nav categories without a page of their own: climb to the nearest page
		for parent != nil && parent.Key == "" {
			parent = b.navParent(parent)
		}
		if parent == nil || parent.Key == e.Key {
			return
		}
		b.edge(Edge{From: PageID(e.Key), To: PageID(parent.Key), Type: EChildOf, Derivation: Explicit, File: e.File, Line: e.Line, Span: "navigation: " + nav.Source})
	})
}

func (b *builder) navParent(target *crawl.NavEntry) *crawl.NavEntry {
	var found *crawl.NavEntry
	b.s.Nav.Walk(func(e *crawl.NavEntry, _ int, parent *crawl.NavEntry) {
		if e == target {
			found = parent
		}
	})
	return found
}

var versionSeg = regexp.MustCompile(`^v?\d+(\.\d+){0,2}(\.x)?$`)

func (b *builder) metadata() {
	repo := b.opt.Repo
	if repo == "" && b.s.Root != "" {
		repo = path.Base(b.s.Root)
	}
	if repo != "" {
		b.g.add(&Node{ID: RepoID(repo), Type: TRepo, Label: repo})
	}
	for _, p := range b.s.Pages {
		if p.Err != "" {
			continue
		}
		from := PageID(p.Key)
		f := fileOf(p)
		if repo != "" {
			b.edge(Edge{From: from, To: RepoID(repo), Type: EInRepo, Derivation: Explicit, File: f})
		}
		for _, t := range p.Tags {
			label := t
			if c, ok, _ := b.opt.Vocab.Canonical(t); ok {
				label = c
			}
			b.g.add(&Node{ID: TagID(label), Type: TTag, Label: label})
			b.edge(Edge{From: from, To: TagID(label), Type: ETaggedWith, Derivation: Explicit, File: f, Line: fmLine(p, "tags", "keywords"), Span: t})
		}
		ct, inferred := doctype.Classify(p, b.opt.ContentTypes, crawl.Match)
		b.g.add(&Node{ID: TypeID(ct), Type: TType, Label: ct})
		if inferred {
			b.edge(Edge{From: from, To: TypeID(ct), Type: EHasType, Derivation: Inferred, File: f, Confidence: 0.6, Span: "heuristic from path, title and page shape"})
		} else {
			b.edge(Edge{From: from, To: TypeID(ct), Type: EHasType, Derivation: Explicit, File: f, Line: fmLine(p, "content_type", "contentType", "doc_type", "type"), Span: p.Type})
		}
		if n := b.g.Node(from); n != nil {
			n.Props["content_type"] = ct
		}
		for _, a := range p.Audience {
			b.g.add(&Node{ID: AudienceID(a), Type: TAudience, Label: a})
			b.edge(Edge{From: from, To: AudienceID(a), Type: EForAudience, Derivation: Explicit, File: f, Line: fmLine(p, "audience"), Span: a})
		}
		if p.Version != "" {
			b.g.add(&Node{ID: VersionID(p.Version), Type: TVersion, Label: p.Version})
			b.edge(Edge{From: from, To: VersionID(p.Version), Type: EInVersion, Derivation: Explicit, File: f, Line: fmLine(p, "version", "versions"), Span: p.Version})
		} else {
			for _, seg := range strings.Split(strings.Trim(p.Key, "/"), "/") {
				if versionSeg.MatchString(seg) {
					b.g.add(&Node{ID: VersionID(seg), Type: TVersion, Label: seg})
					b.edge(Edge{From: from, To: VersionID(seg), Type: EInVersion, Derivation: Extracted, File: f, Span: "path segment " + seg})
					break
				}
			}
		}
		if p.Owner != "" {
			for _, o := range strings.Fields(p.Owner) {
				b.g.add(&Node{ID: OwnerID(o), Type: TOwner, Label: o})
				src, line := f, fmLine(p, "owner", "owners", "author", "maintainer")
				if _, inFM := p.FrontMatter["owner"]; !inFM {
					if _, inFM = p.FrontMatter["owners"]; !inFM {
						src, line = "CODEOWNERS", 0
					}
				}
				b.edge(Edge{From: from, To: OwnerID(o), Type: EOwnedBy, Derivation: Explicit, File: src, Line: line, Span: o})
			}
		}
		for _, key := range []string{"supersedes", "replaces"} {
			for _, target := range toStrings(p.FrontMatter[key]) {
				t := b.s.Resolve(p, target)
				if t.Page != nil {
					b.edge(Edge{From: from, To: PageID(t.Page.Key), Type: ESupersedes, Derivation: Explicit, File: f, Line: fmLine(p, key), Span: target})
				}
			}
		}
	}
}

func toStrings(v any) []string {
	switch v := v.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func fmLine(p *model.Page, keys ...string) int {
	lines := strings.Split(string(p.Raw), "\n")
	for i, l := range lines {
		if i > 0 && i >= p.FrontMatterEnd {
			break
		}
		for _, k := range keys {
			if strings.HasPrefix(l, k+":") || strings.HasPrefix(l, k+" =") || strings.HasPrefix(l, k+"=") {
				return i + 1
			}
		}
	}
	return 0
}

func (b *builder) entities() {
	s := b.s
	// 1. which commands belong to the product: named in config, in the
	// vocabulary, or used as the first word of shell lines on at least two pages
	known := map[string]bool{}
	for _, c := range b.opt.Commands {
		known[c] = true
	}
	if len(known) == 0 {
		count := map[string]map[string]bool{}
		for _, p := range s.Pages {
			for _, cb := range p.CodeBlocks {
				if !shellLangs[strings.ToLower(cb.Lang)] {
					continue
				}
				for _, line := range strings.Split(cb.Content, "\n") {
					for _, c := range commandsIn(line, strings.ToLower(cb.Lang), nil) {
						if count[c.base] == nil {
							count[c.base] = map[string]bool{}
						}
						count[c.base][p.Key] = true
					}
				}
			}
		}
		// a command is the product's when a page title or heading names it,
		// or it is used on at least 30% as many pages as the most used one;
		// npm, git and go in a few install snippets are not the product
		named := map[string]bool{}
		for _, p := range s.Pages {
			named[strings.ToLower(strings.Trim(p.Title, "` "))] = true
			for _, h := range p.Headings {
				named[strings.ToLower(strings.Trim(h.Text, "` "))] = true
			}
		}
		top := 0
		for _, pages := range count {
			top = max(top, len(pages))
		}
		var detected []string
		for base, pages := range count {
			if len(pages) >= 2 && (named[base] || float64(len(pages)) >= 0.3*float64(top)) {
				known[base] = true
				detected = append(detected, base)
			}
		}
		sort.Strings(detected)
		b.g.Meta["commands_detected"] = strings.Join(detected, ",")
		if !b.opt.Vocab.Empty() {
			for name := range b.opt.Vocab.Terms {
				known[strings.ToLower(name)] = true
			}
		}
	}
	keep := func(o occurrence) bool {
		if o.kind == KCommand || o.kind == KFlag {
			return known[strings.Fields(o.name)[0]]
		}
		return true
	}

	type pageOcc struct {
		p   *model.Page
		occ []occurrence
	}
	var all []pageOcc
	for _, p := range s.Pages {
		if p.Err != "" {
			continue
		}
		glossary := strings.Contains(strings.ToLower(p.Key), "glossary") || strings.EqualFold(p.Title, "glossary")
		occ := extractPage(p, known, glossary)
		// a page inside a glossary folder defines the term in its title
		if glossary && !strings.EqualFold(p.Title, "glossary") && p.Title != "" && !crawl.IsIndexFile(p.Path) {
			occ = append(occ, occurrence{KGlossary, p.Title, EDefines, 1, p.Title})
		}
		all = append(all, pageOcc{p, occ})
	}
	// 2. entity nodes, merging aliases from the vocabulary
	entityFor := map[string]string{} // lowercased name -> entity id
	addEntity := func(kind, name string) string {
		canonical := name
		var aliasOf string
		if c, ok, isAlias := b.opt.Vocab.Canonical(name); ok {
			canonical = c
			if isAlias {
				aliasOf = name
			}
			// a vocabulary term is one entity, whatever found it
			kind = KTerm
			if t := b.opt.Vocab.Terms[c]; t != nil && t.Type != "" {
				kind = t.Type
			}
		}
		id := EntityID(kind, canonical)
		n := b.g.add(&Node{ID: id, Type: TEntity, Kind: kind, Label: canonical})
		if aliasOf != "" && !in(aliasOf, n.Aliases) {
			n.Aliases = append(n.Aliases, aliasOf)
		}
		if _, ok := entityFor[strings.ToLower(canonical)]; !ok {
			entityFor[strings.ToLower(canonical)] = id
		}
		entityFor[strings.ToLower(name)] = id
		return id
	}
	if !b.opt.Vocab.Empty() {
		names := make([]string, 0, len(b.opt.Vocab.Terms))
		for n := range b.opt.Vocab.Terms {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			kind := KTerm
			if t := b.opt.Vocab.Terms[name]; t.Type != "" {
				kind = t.Type
			}
			id := addEntity(kind, name)
			for _, a := range b.opt.Vocab.Terms[name].Aliases {
				if n := b.g.Node(id); !in(a, n.Aliases) {
					n.Aliases = append(n.Aliases, a)
				}
			}
			if d := b.opt.Vocab.Terms[name].Description; d != "" {
				b.g.Node(id).Props = map[string]string{"description": d}
			}
		}
	}
	for _, po := range all {
		for _, o := range po.occ {
			if o.kind == "" || !keep(o) {
				continue
			}
			id := addEntity(o.kind, o.name)
			b.edge(Edge{From: PageID(po.p.Key), To: id, Type: o.rel, Derivation: Extracted, File: fileOf(po.p), Line: o.line, Span: o.span})
		}
	}
	// 3. bare inline code that names a known entity
	for _, po := range all {
		for _, o := range po.occ {
			if o.kind != "" {
				continue
			}
			if id, ok := entityFor[strings.ToLower(o.name)]; ok {
				b.edge(Edge{From: PageID(po.p.Key), To: id, Type: EMentions, Derivation: Extracted, File: fileOf(po.p), Line: o.line, Span: o.span})
			}
		}
	}
	// 4. vocabulary terms in prose, recording which spelling was used
	if !b.opt.Vocab.Empty() {
		b.termMentions()
	}
	// 5. pages about an entity: the title names it
	b.titleEdges()
	// 6. suspected duplicates: names that differ only in case or separators
	groups := map[string][]string{}
	for _, n := range b.g.Nodes {
		if n.Type == TEntity {
			k := n.Kind + ":" + squash(n.Label)
			groups[k] = append(groups[k], n.ID)
		}
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if ids := groups[k]; len(ids) > 1 {
			sort.Strings(ids)
			var labels []string
			for _, id := range ids {
				labels = append(labels, b.g.Node(id).Label)
			}
			b.g.Reviews = append(b.g.Reviews, Review{Kind: "suspected-duplicate-entity", Nodes: ids,
				Message: fmt.Sprintf("%s may be one %s; add aliases to the vocabulary file to merge them", strings.Join(labels, ", "), strings.SplitN(k, ":", 2)[0])})
		}
	}
}

func squash(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', ' ', '.', '`':
			return -1
		}
		if r >= 'A' && r <= 'Z' {
			return r + 32
		}
		return r
	}, s)
}

// termMentions finds vocabulary terms (and their aliases) in titles, headings and prose.
func (b *builder) termMentions() {
	type spelling struct {
		re  *regexp.Regexp
		raw string
		id  string
	}
	var sp []spelling
	for _, raw := range b.opt.Vocab.Spellings() {
		c, _, _ := b.opt.Vocab.Canonical(raw)
		kind := KTerm
		if t := b.opt.Vocab.Terms[c]; t != nil && t.Type != "" {
			kind = t.Type
		}
		flags := "(?i)"
		if len(raw) <= 4 && strings.ToUpper(raw) == raw {
			flags = "" // short acronyms are case-sensitive
		}
		sp = append(sp, spelling{regexp.MustCompile(flags + `(^|[^\w-])` + regexp.QuoteMeta(raw) + `($|[^\w-])`), raw, EntityID(kind, c)})
	}
	if b.g.Meta == nil {
		b.g.Meta = map[string]string{}
	}
	for _, p := range b.s.Pages {
		if p.Err != "" {
			continue
		}
		texts := []model.Paragraph{{Text: p.Title, Line: 1}}
		for _, h := range p.Headings {
			texts = append(texts, model.Paragraph{Text: h.Text, Line: h.Line})
		}
		texts = append(texts, p.Paragraphs...)
		for _, t := range texts {
			for _, s := range sp {
				loc := s.re.FindStringSubmatchIndex(t.Text)
				if loc == nil {
					continue
				}
				used := matchedTerm(t.Text, loc)
				b.edge(Edge{From: PageID(p.Key), To: s.id, Type: EMentions, Derivation: Extracted, File: fileOf(p), Line: t.Line, Span: excerpt(t.Text, loc[0], loc[1])})
				b.spelling(s.id, used)
			}
		}
	}
}

// matchedTerm returns the text between the two boundary groups of a term match.
func matchedTerm(text string, loc []int) string {
	return text[loc[3]:loc[4]]
}

func (b *builder) spelling(id, used string) {
	n := b.g.Node(id)
	if n == nil {
		return
	}
	if n.Props == nil {
		n.Props = map[string]string{}
	}
	key := "spelling:" + used
	n.Props[key] = fmt.Sprint(atoi(n.Props[key]) + 1)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func excerpt(s string, from, to int) string {
	start := max(0, from-50)
	end := min(len(s), to+50)
	out := s[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(s) {
		out += "…"
	}
	return out
}

func (b *builder) titleEdges() {
	type ent struct {
		id, name string
		re       *regexp.Regexp
	}
	var ents []ent
	for _, n := range b.g.Nodes {
		if n.Type != TEntity || len(n.Label) < 3 {
			continue
		}
		ents = append(ents, ent{n.ID, strings.ToLower(n.Label), regexp.MustCompile(`(?i)(^|[^\w-])` + regexp.QuoteMeta(n.Label) + `($|[^\w-])`)})
	}
	for _, p := range b.s.Pages {
		if p.Err != "" {
			continue
		}
		for _, h := range p.Headings {
			if h.Level < 2 {
				continue
			}
			text := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(h.Text, "`", "")))
			for _, e := range ents {
				if text == e.name {
					b.edge(Edge{From: PageID(p.Key), To: e.id, Type: EDocuments, Derivation: Extracted, File: fileOf(p), Line: h.Line, Span: "heading: " + h.Text})
				}
			}
		}
		if p.Title == "" {
			continue
		}
		title := strings.ToLower(strings.ReplaceAll(p.Title, "`", ""))
		for _, e := range ents {
			switch {
			case title == e.name:
				b.edge(Edge{From: PageID(p.Key), To: e.id, Type: EDefines, Derivation: Extracted, File: fileOf(p), Line: 1, Span: "title: " + p.Title})
				b.edge(Edge{From: PageID(p.Key), To: e.id, Type: EDocuments, Derivation: Extracted, File: fileOf(p), Line: 1, Span: "title: " + p.Title})
			case len(e.name) >= 4 && strings.Contains(title, e.name) && e.re.MatchString(title):
				b.edge(Edge{From: PageID(p.Key), To: e.id, Type: EDocuments, Derivation: Extracted, File: fileOf(p), Line: 1, Span: "title: " + p.Title})
			}
		}
	}
}

func (b *builder) modelExtraction() {
	for _, p := range b.s.Pages {
		if p.Err != "" {
			continue
		}
		edges, nodes, err := b.opt.Extractor.Extract(p)
		if err != nil {
			b.g.Meta["extractor_error"] = err.Error()
			continue
		}
		for _, n := range nodes {
			b.g.add(n)
		}
		for _, e := range edges {
			if e.Span == "" || !strings.Contains(string(p.Raw), e.Span) {
				continue // untraceable: dropped
			}
			k := e.From + "|" + e.Type + "|" + e.To
			if b.seen[k] {
				continue // never override an explicit or extracted edge
			}
			e.Derivation = Inferred
			if e.File == "" {
				e.File = fileOf(p)
			}
			b.edge(e)
		}
	}
}
