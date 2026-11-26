// Package parse turns Markdown, MDX, HTML and AsciiDoc documents into model.Page values.
package parse

import (
	"bytes"
	"regexp"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"

	"github.com/docsbot/docsbot/internal/model"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.DefinitionList, extension.Footnote),
	goldmark.WithParserOptions(parser.WithAttribute()),
)

// Options adjusts parsing to match a site generator's renderer.
type Options struct {
	// DefinitionTermIDs gives definition list terms anchors, like Hugo's
	// markup.goldmark.parser.autoDefinitionTermID.
	DefinitionTermIDs bool
	// AnchorStyle is the heading slug style: github (default), mkdocs, sphinx, kramdown.
	AnchorStyle string
	// MDXLinkProps are JSX props that hold link targets, e.g. href and to.
	MDXLinkProps []string
}

var (
	shortcode    = regexp.MustCompile(`\{\{[<%].*?[%>]\}\}`)
	explicitID   = regexp.MustCompile(`\s*\{#([^}\s]+)[^}]*\}\s*$`)
	htmlIDAttr   = regexp.MustCompile(`(?i)\s(?:id|name)\s*=\s*["']([^"']+)["']`)
	htmlTagRe    = regexp.MustCompile(`(?is)<(a|img)\b([^>]*)>`)
	hrefAttrRe   = regexp.MustCompile(`(?i)\b(href|src)\s*=\s*["']([^"']*)["']`)
	altAttrRe    = regexp.MustCompile(`(?i)\balt\s*=\s*["']([^"']*)["']`)
	includeShort = regexp.MustCompile(`\{\{[<%]-?\s*(?:include|readfile)\s+(?:file=)?"([^"]+)"[^}]*[%>]\}\}`)
	mdxImport    = regexp.MustCompile(`(?m)^import\s+[\w{}\s,*]+\s+from\s+['"]([^'"]+\.mdx?)['"]`)
	snippetRe    = regexp.MustCompile(`(?m)^\s*-{2}8<-{2}\s+["']([^"']+)["']`)
)

// Markdown parses a markdown document (with optional front matter).
func Markdown(src []byte) *model.Page { return MarkdownWith(src, Options{}) }

// MarkdownWith parses markdown with renderer options.
func MarkdownWith(src []byte, opt Options) *model.Page {
	fm, body, offset := SplitFrontMatter(src)
	p := &model.Page{
		Format:         "markdown",
		Title:          fm.Title,
		Description:    fm.Description,
		Aliases:        fm.Aliases,
		Draft:          fm.Draft,
		Anchors:        map[string]bool{},
		Chars:          len(body),
		FrontMatter:    fm.Raw,
		FrontMatterEnd: fm.Lines,
		Tags:           fm.Tags,
		Type:           fm.Type,
		Audience:       fm.Audience,
		Owner:          fm.Owner,
		Version:        fm.Version,
		DocStatus:      fm.Status,
		Raw:            src,
	}
	if fm.Err != nil {
		p.FrontMatterErr = fm.Err.Error()
	}
	if p.Title == "" {
		p.Title = fm.LinkTitle
	}
	props := opt.MDXLinkProps
	if len(props) == 0 {
		props = []string{"href", "to"}
	}
	jsxProp := regexp.MustCompile(`\b(?:` + strings.Join(quoteAll(props), "|") + `)=\{?["']([^"']+)["']\}?`)
	lines := newLineIndex(body, offset)
	doc := md.Parser().Parse(text.NewReader(body))
	slugs := newSlugger(opt.AnchorStyle)
	section := ""

	addHTML := func(raw string, pos int) {
		line, col := lines.of(pos), lines.col(pos)
		for _, m := range htmlIDAttr.FindAllStringSubmatch(raw, -1) {
			p.Anchors[m[1]] = true
		}
		seen := map[string]bool{}
		for _, m := range htmlTagRe.FindAllStringSubmatch(raw, -1) {
			attrs := m[2]
			h := hrefAttrRe.FindStringSubmatch(attrs)
			if h == nil {
				continue
			}
			seen[h[2]] = true
			l := model.Link{Href: h[2], Line: line, Column: col, Section: section, Image: strings.EqualFold(m[1], "img")}
			if l.Image {
				if a := altAttrRe.FindStringSubmatch(attrs); a != nil {
					l.Text = a[1]
				}
			} else {
				l.Text = "(html)"
			}
			p.Links = append(p.Links, l)
		}
		for _, m := range jsxProp.FindAllStringSubmatch(raw, -1) {
			if !seen[m[1]] {
				p.Links = append(p.Links, model.Link{Href: m[1], Text: "(component)", Line: line, Column: col, Section: section})
			}
		}
	}

	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			txt := strings.TrimSpace(shortcode.ReplaceAllString(nodeText(n, body), ""))
			anchor := ""
			if id, ok := n.AttributeString("id"); ok {
				if b, ok := id.([]byte); ok {
					anchor = string(b)
				}
			}
			if m := explicitID.FindStringSubmatch(txt); m != nil {
				anchor = m[1]
				txt = strings.TrimSpace(explicitID.ReplaceAllString(txt, ""))
			}
			if anchor == "" {
				anchor = slugs.slug(txt)
			} else {
				slugs.reserve(anchor)
			}
			if anchor != "" {
				p.Anchors[anchor] = true
			}
			section = anchor
			p.Headings = append(p.Headings, model.Heading{Level: n.Level, Text: txt, Anchor: anchor, Line: lines.of(blockStart(n))})
			if p.Title == "" && n.Level == 1 {
				p.Title = txt
			}
		case *ast.Link:
			pos := inlineStart(n, body)
			p.Links = append(p.Links, model.Link{Href: string(n.Destination), Text: nodeText(n, body), Line: lines.of(pos), Column: lines.col(pos), Section: section})
		case *ast.Image:
			pos := inlineStart(n, body)
			p.Links = append(p.Links, model.Link{Href: string(n.Destination), Text: nodeText(n, body), Line: lines.of(pos), Column: lines.col(pos), Section: section, Image: true})
		case *ast.AutoLink:
			if n.AutoLinkType == ast.AutoLinkURL {
				pos := inlineStart(n, body)
				p.Links = append(p.Links, model.Link{Href: string(n.URL(body)), Text: string(n.Label(body)), Line: lines.of(pos), Column: lines.col(pos), Section: section})
			}
		case *ast.CodeSpan:
			p.InlineCode = append(p.InlineCode, model.Span{Text: nodeText(n, body), Line: lines.of(inlineStart(n, body))})
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock:
			p.CodeBlocks = append(p.CodeBlocks, model.CodeBlock{Lang: string(n.Language(body)), Lines: n.Lines().Len(), Line: fenceLine(n, body, lines), Content: blockText(n, body), Fenced: true})
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock:
			p.CodeBlocks = append(p.CodeBlocks, model.CodeBlock{Lines: n.Lines().Len(), Line: lines.of(blockStart(n)), Content: blockText(n, body)})
			return ast.WalkSkipChildren, nil
		case *ast.Paragraph:
			if t := strings.TrimSpace(shortcode.ReplaceAllString(nodeText(n, body), "")); t != "" {
				p.Paragraphs = append(p.Paragraphs, model.Paragraph{Text: t, Line: lines.of(blockStart(n)), Section: section})
			}
		case *ast.List:
			line := 0
			if c := n.FirstChild(); c != nil && c.FirstChild() != nil {
				line = lines.of(blockStart(c.FirstChild()))
			}
			p.Lists = append(p.Lists, model.List{Ordered: n.IsOrdered(), Items: n.ChildCount(), Line: line})
		case *east.Table:
			p.Tables++
		case *east.TableRow:
			if c := n.FirstChild(); c != nil {
				if t := strings.TrimSpace(nodeText(c, body)); t != "" {
					p.TableCells = append(p.TableCells, model.Span{Text: t, Line: lines.of(inlineStart(c, body))})
				}
			}
		case *east.DefinitionTerm:
			t := nodeText(n, body)
			p.DefTerms = append(p.DefTerms, model.Span{Text: t, Line: lines.of(blockStart(n))})
			if opt.DefinitionTermIDs {
				p.Anchors[slugs.slug(t)] = true
			}
		case *ast.HTMLBlock:
			addHTML(blockText(n, body), blockStart(n))
		case *ast.RawHTML:
			var buf bytes.Buffer
			for i := 0; i < n.Segments.Len(); i++ {
				s := n.Segments.At(i)
				buf.Write(s.Value(body))
			}
			start := -1
			if n.Segments.Len() > 0 {
				start = n.Segments.At(0).Start
			}
			addHTML(buf.String(), start)
		case *east.FootnoteLink, *east.FootnoteBacklink:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	for _, re := range []struct {
		re   *regexp.Regexp
		kind string
	}{{includeShort, "shortcode"}, {mdxImport, "import"}, {snippetRe, "snippet"}} {
		for _, m := range re.re.FindAllSubmatchIndex(body, -1) {
			p.Includes = append(p.Includes, model.Include{Target: string(body[m[2]:m[3]]), Line: lines.of(m[0]), Kind: re.kind})
		}
	}
	// empty ATX headings ("## ") carry no source segment; place them by scanning
	emptyATX := regexp.MustCompile(`(?m)^ {0,3}#{1,6}[ \t]*$`)
	locs := emptyATX.FindAllIndex(body, -1)
	j := 0
	for i := range p.Headings {
		if p.Headings[i].Text == "" && p.Headings[i].Line == 0 && j < len(locs) {
			p.Headings[i].Line = lines.of(locs[j][0])
			j++
		}
	}
	p.Words = countWords(body)
	return p
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = regexp.QuoteMeta(s)
	}
	return out
}

func blockText(n ast.Node, src []byte) string {
	var buf bytes.Buffer
	for i := 0; i < n.Lines().Len(); i++ {
		s := n.Lines().At(i)
		buf.Write(s.Value(src))
	}
	return buf.String()
}

func nodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				b.Write(c.Segment.Value(src))
				if c.SoftLineBreak() || c.HardLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(c.Value)
			case *ast.RawHTML:
				// skip inline tags
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return strings.TrimSpace(b.String())
}

// blockStart returns the source offset of a block node, or -1.
func blockStart(n ast.Node) int {
	if l := n.Lines(); l != nil && l.Len() > 0 {
		return l.At(0).Start
	}
	if c := n.FirstChild(); c != nil {
		if t, ok := c.(*ast.Text); ok {
			return t.Segment.Start
		}
		return blockStart(c)
	}
	return -1
}

// inlineStart finds the source offset where an inline node starts. For links
// and images it walks back from the first text segment to the opening bracket.
func inlineStart(n ast.Node, src []byte) int {
	pos := -1
	var find func(ast.Node) bool
	find = func(n ast.Node) bool {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				pos = t.Segment.Start
				return true
			}
			if find(c) {
				return true
			}
		}
		return false
	}
	if find(n) {
		switch n.(type) {
		case *ast.Link:
			if i := bytes.LastIndexByte(src[:pos], '['); i >= 0 && pos-i < 4 {
				return i
			}
		case *ast.Image:
			if i := bytes.LastIndex(src[:pos], []byte("![")); i >= 0 && pos-i < 5 {
				return i
			}
		case *ast.CodeSpan:
			for pos > 0 && src[pos-1] == '`' {
				pos--
			}
		case *ast.AutoLink:
			if pos > 0 && src[pos-1] == '<' {
				return pos - 1
			}
		}
		return pos
	}
	if a, ok := n.(*ast.AutoLink); ok {
		_ = a
	}
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Type() == ast.TypeBlock {
			return blockStart(p)
		}
	}
	return -1
}

type lineIndex struct {
	nl     []int
	offset int
}

func newLineIndex(src []byte, offset int) lineIndex {
	var nl []int
	for i, c := range src {
		if c == '\n' {
			nl = append(nl, i)
		}
	}
	return lineIndex{nl: nl, offset: offset}
}

func (li lineIndex) of(pos int) int {
	if pos < 0 {
		return 0
	}
	return sort.SearchInts(li.nl, pos) + 1 + li.offset
}

// col returns the 1-based byte column of pos.
func (li lineIndex) col(pos int) int {
	if pos < 0 {
		return 0
	}
	i := sort.SearchInts(li.nl, pos)
	if i == 0 {
		return pos + 1
	}
	return pos - li.nl[i-1]
}

var fenceRe = regexp.MustCompile("(?ms)^\\s*(```|~~~).*?^\\s*(```|~~~)")

func countWords(body []byte) int {
	return len(strings.Fields(fenceRe.ReplaceAllString(string(body), " ")))
}

// fenceLine finds the line of a fenced block's opening fence.
func fenceLine(n *ast.FencedCodeBlock, src []byte, li lineIndex) int {
	if n.Info != nil {
		return li.of(n.Info.Segment.Start)
	}
	if st := blockStart(n); st >= 0 {
		return li.of(st) - 1
	}
	return 0
}
