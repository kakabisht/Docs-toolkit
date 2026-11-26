package parse

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/docsbot/docsbot/internal/model"
)

// HTML parses an HTML page. Links are taken from the main content region
// (<main>, <article> or [role=main]) when one exists, so site-wide navigation
// does not drown out the real link structure. Anchors come from the whole page.
func HTML(src []byte) *model.Page {
	p := &model.Page{Format: "html", Anchors: map[string]bool{}, Chars: len(src)}
	doc, err := html.Parse(bytes.NewReader(src))
	if err != nil {
		p.Err = err.Error()
		return p
	}
	var title, h1 string
	var mainNode *html.Node
	var walkAll func(*html.Node)
	walkAll = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if id := attr(n, "id"); id != "" {
				p.Anchors[id] = true
			}
			if n.DataAtom == atom.A {
				if name := attr(n, "name"); name != "" {
					p.Anchors[name] = true
				}
			}
			switch n.DataAtom {
			case atom.Title:
				if title == "" {
					title = strings.TrimSpace(textOf(n))
				}
			case atom.Meta:
				if strings.EqualFold(attr(n, "name"), "description") && p.Description == "" {
					p.Description = attr(n, "content")
				}
			case atom.Main:
				if mainNode == nil {
					mainNode = n
				}
			case atom.Article:
				if mainNode == nil {
					mainNode = n
				}
			}
			if mainNode == nil && strings.EqualFold(attr(n, "role"), "main") {
				mainNode = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walkAll(c)
		}
	}
	walkAll(doc)

	root := mainNode
	if root == nil {
		root = findAtom(doc, atom.Body)
		if root == nil {
			root = doc
		}
	}
	slugs := slugger{}
	section := ""
	var words int
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			words += len(strings.Fields(n.Data))
		case html.ElementNode:
			switch n.DataAtom {
			case atom.Script, atom.Style, atom.Noscript, atom.Template:
				return
			case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
				lvl := int(n.Data[1] - '0')
				txt := strings.Join(strings.Fields(textOf(n)), " ")
				txt = strings.TrimSuffix(strings.TrimSuffix(txt, "#"), "¶")
				txt = strings.TrimSpace(txt)
				anchor := attr(n, "id")
				if anchor == "" {
					// heading anchors are often on a child <a id> or parent <section id>
					if a := findAttrDesc(n, "id"); a != "" {
						anchor = a
					} else {
						anchor = slugs.unique(Slugify(txt))
					}
				}
				p.Anchors[anchor] = true
				section = anchor
				p.Headings = append(p.Headings, model.Heading{Level: lvl, Text: txt, Anchor: anchor})
				if lvl == 1 && h1 == "" {
					h1 = txt
				}
			case atom.A:
				if href, ok := attrOK(n, "href"); ok {
					p.Links = append(p.Links, model.Link{Href: strings.TrimSpace(href), Text: strings.Join(strings.Fields(textOf(n)), " "), Section: section})
				}
			case atom.Img:
				if src := attr(n, "src"); src != "" {
					p.Links = append(p.Links, model.Link{Href: src, Text: attr(n, "alt"), Section: section, Image: true})
				}
			case atom.Pre:
				lang := ""
				for _, cls := range strings.Fields(attr(n, "class") + " " + codeClass(n)) {
					if l, ok := strings.CutPrefix(cls, "language-"); ok {
						lang = l
						break
					}
					if l, ok := strings.CutPrefix(cls, "lang-"); ok {
						lang = l
						break
					}
				}
				if lang == "" {
					lang = attr(n, "data-lang")
				}
				p.CodeBlocks = append(p.CodeBlocks, model.CodeBlock{Lang: lang, Lines: strings.Count(strings.TrimRight(textOf(n), "\n"), "\n") + 1})
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	if mainNode != nil {
		if body := findAtom(doc, atom.Body); body != nil {
			collectNav(body, mainNode, p)
		}
	}
	p.Words = words
	p.Title = h1
	if p.Title == "" {
		p.Title = title
	}
	return p
}

// collectNav records links outside the main content region as navigation links.
func collectNav(n, skip *html.Node, p *model.Page) {
	if n == skip {
		return
	}
	if n.Type == html.ElementNode && n.DataAtom == atom.A {
		if href, ok := attrOK(n, "href"); ok {
			p.Links = append(p.Links, model.Link{Href: strings.TrimSpace(href), Text: strings.Join(strings.Fields(textOf(n)), " "), Nav: true})
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		collectNav(c, skip, p)
	}
}

func codeClass(pre *html.Node) string {
	for c := pre.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.DataAtom == atom.Code {
			return attr(c, "class") + " " + attr(c, "data-lang")
		}
	}
	// the class is sometimes on a wrapping div (highlight-go, language-go)
	if pre.Parent != nil {
		return attr(pre.Parent, "class")
	}
	return ""
}

func attr(n *html.Node, key string) string {
	v, _ := attrOK(n, key)
	return v
}

func attrOK(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val, true
		}
	}
	return "", false
}

func findAttrDesc(n *html.Node, key string) string {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			if v := attr(c, key); v != "" {
				return v
			}
			if v := findAttrDesc(c, key); v != "" {
				return v
			}
		}
	}
	return ""
}

func findAtom(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if f := findAtom(c, a); f != nil {
			return f
		}
	}
	return nil
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		if n.Type == html.ElementNode && (n.DataAtom == atom.Script || n.DataAtom == atom.Style) {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
