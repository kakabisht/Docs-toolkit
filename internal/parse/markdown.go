// Package parse turns markdown and HTML documents into model.Page values.
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
	"gopkg.in/yaml.v3"

	"github.com/docsbot/docsbot/internal/model"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.DefinitionList, extension.Footnote),
	goldmark.WithParserOptions(parser.WithAttribute()),
)

// FrontMatter holds the fields docsbot reads from YAML front matter.
type FrontMatter struct {
	Title       string   `yaml:"title"`
	LinkTitle   string   `yaml:"linkTitle"`
	Description string   `yaml:"description"`
	Aliases     []string `yaml:"aliases"`
	Slug        string   `yaml:"slug"`
	URL         string   `yaml:"url"`
	Permalink   string   `yaml:"permalink"`
	Draft       bool     `yaml:"draft"`
}

var (
	tomlTitle = regexp.MustCompile(`(?m)^title\s*=\s*["'](.*)["']\s*$`)
	tomlDesc  = regexp.MustCompile(`(?m)^description\s*=\s*["'](.*)["']\s*$`)
	tomlSlug  = regexp.MustCompile(`(?m)^slug\s*=\s*["'](.*)["']\s*$`)
	tomlURL   = regexp.MustCompile(`(?m)^url\s*=\s*["'](.*)["']\s*$`)
	tomlDraft = regexp.MustCompile(`(?m)^draft\s*=\s*true\s*$`)
)

// SplitFrontMatter separates YAML (---) or TOML (+++) front matter from the body.
// It returns the parsed fields, the body and the number of lines removed.
func SplitFrontMatter(src []byte) (FrontMatter, []byte, int) {
	var fm FrontMatter
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	for _, delim := range []string{"---", "+++"} {
		if !bytes.HasPrefix(src, []byte(delim+"\n")) && !bytes.HasPrefix(src, []byte(delim+"\r\n")) {
			continue
		}
		rest := src[len(delim)+1:]
		end := bytes.Index(rest, []byte("\n"+delim))
		if end < 0 {
			return fm, src, 0
		}
		head := rest[:end]
		body := rest[end+1+len(delim):]
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			body = body[i+1:]
		} else {
			body = nil
		}
		if delim == "---" {
			_ = yaml.Unmarshal(head, &fm)
		} else {
			fm.Title = firstSub(tomlTitle, head)
			fm.Description = firstSub(tomlDesc, head)
			fm.Slug = firstSub(tomlSlug, head)
			fm.URL = firstSub(tomlURL, head)
			fm.Draft = tomlDraft.Match(head)
		}
		lines := bytes.Count(src[:len(src)-len(body)], []byte("\n"))
		return fm, body, lines
	}
	return fm, src, 0
}

func firstSub(re *regexp.Regexp, b []byte) string {
	if m := re.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

var (
	shortcode    = regexp.MustCompile(`\{\{[<%].*?[%>]\}\}`)
	explicitID   = regexp.MustCompile(`\s*\{#([^}\s]+)\}\s*$`)
	htmlIDAttr   = regexp.MustCompile(`(?i)\s(?:id|name)\s*=\s*["']([^"']+)["']`)
	htmlHrefAttr = regexp.MustCompile(`(?i)<(a|img)\s[^>]*?(?:href|src)\s*=\s*["']([^"']+)["']`)
	mdxJSXHref   = regexp.MustCompile(`(?i)\b(?:href|to)=\{?["']([^"']+)["']\}?`)
)

// Options adjusts markdown parsing to match a site generator's renderer.
type Options struct {
	// DefinitionTermIDs gives definition list terms anchors, like Hugo's
	// markup.goldmark.parser.autoDefinitionTermID.
	DefinitionTermIDs bool
}

// Markdown parses a markdown document (with optional front matter).
func Markdown(src []byte) *model.Page { return MarkdownWith(src, Options{}) }

// MarkdownWith parses markdown with renderer options.
func MarkdownWith(src []byte, opt Options) *model.Page {
	fm, body, offset := SplitFrontMatter(src)
	p := &model.Page{
		Format:      "markdown",
		Title:       fm.Title,
		Description: fm.Description,
		Aliases:     fm.Aliases,
		Draft:       fm.Draft,
		Anchors:     map[string]bool{},
		Chars:       len(body),
	}
	lines := newLineIndex(body, offset)
	doc := md.Parser().Parse(text.NewReader(body))
	slugs := slugger{}
	section := ""

	addHTML := func(raw string, line int) {
		for _, m := range htmlIDAttr.FindAllStringSubmatch(raw, -1) {
			p.Anchors[m[1]] = true
		}
		for _, m := range htmlHrefAttr.FindAllStringSubmatch(raw, -1) {
			p.Links = append(p.Links, model.Link{Href: m[2], Line: line, Section: section, Image: strings.EqualFold(m[1], "img")})
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
				anchor = slugs.unique(Slugify(txt))
			} else {
				slugs[anchor]++
			}
			p.Anchors[anchor] = true
			section = anchor
			p.Headings = append(p.Headings, model.Heading{Level: n.Level, Text: txt, Anchor: anchor, Line: lines.of(blockStart(n))})
			if p.Title == "" && n.Level == 1 {
				p.Title = txt
			}
		case *east.DefinitionTerm:
			if opt.DefinitionTermIDs {
				p.Anchors[slugs.unique(Slugify(nodeText(n, body)))] = true
			}
		case *ast.Link:
			p.Links = append(p.Links, model.Link{Href: string(n.Destination), Text: nodeText(n, body), Line: lines.of(inlineStart(n)), Section: section})
		case *ast.Image:
			p.Links = append(p.Links, model.Link{Href: string(n.Destination), Text: nodeText(n, body), Line: lines.of(inlineStart(n)), Section: section, Image: true})
		case *ast.AutoLink:
			if n.AutoLinkType == ast.AutoLinkURL {
				p.Links = append(p.Links, model.Link{Href: string(n.URL(body)), Line: lines.of(inlineStart(n)), Section: section})
			}
		case *ast.FencedCodeBlock:
			p.CodeBlocks = append(p.CodeBlocks, model.CodeBlock{Lang: string(n.Language(body)), Lines: n.Lines().Len(), Line: lines.of(blockStart(n))})
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock:
			p.CodeBlocks = append(p.CodeBlocks, model.CodeBlock{Lines: n.Lines().Len(), Line: lines.of(blockStart(n))})
			return ast.WalkSkipChildren, nil
		case *ast.HTMLBlock:
			var buf bytes.Buffer
			for i := 0; i < n.Lines().Len(); i++ {
				s := n.Lines().At(i)
				buf.Write(s.Value(body))
			}
			raw := buf.String()
			addHTML(raw, lines.of(blockStart(n)))
			for _, m := range mdxJSXHref.FindAllStringSubmatch(raw, -1) {
				if !strings.Contains(raw, `href="`+m[1]+`"`) && !strings.Contains(raw, `href='`+m[1]+`'`) {
					p.Links = append(p.Links, model.Link{Href: m[1], Line: lines.of(blockStart(n)), Section: section})
				}
			}
		case *ast.RawHTML:
			var buf bytes.Buffer
			for i := 0; i < n.Segments.Len(); i++ {
				s := n.Segments.At(i)
				buf.Write(s.Value(body))
			}
			addHTML(buf.String(), lines.of(inlineStart(n)))
		case *east.FootnoteLink, *east.FootnoteBacklink:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	p.Words = countWords(body)
	return p
}

func nodeText(n ast.Node, src []byte) string {
	var b strings.Builder
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch c := c.(type) {
			case *ast.Text:
				b.Write(c.Segment.Value(src))
				if c.SoftLineBreak() {
					b.WriteByte(' ')
				}
			case *ast.String:
				b.Write(c.Value)
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
	return -1
}

// inlineStart finds a source offset for an inline node via its text or nearest block.
func inlineStart(n ast.Node) int {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			return t.Segment.Start
		}
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

var fenceRe = regexp.MustCompile("(?ms)^\\s*(```|~~~).*?^\\s*(```|~~~)")

func countWords(body []byte) int {
	return len(strings.Fields(fenceRe.ReplaceAllString(string(body), " ")))
}
