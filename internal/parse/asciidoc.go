package parse

import (
	"regexp"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/model"
)

var (
	adocHeading    = regexp.MustCompile(`^(={1,6})\s+(.+?)\s*$`)
	adocAttr       = regexp.MustCompile(`^:([A-Za-z0-9_][A-Za-z0-9_-]*!?):\s*(.*)$`)
	adocBlockID    = regexp.MustCompile(`^\[\[([^\],]+)(?:,[^\]]*)?\]\]\s*$|^\[#([^\].,]+)[^\]]*\]\s*$`)
	adocInlineID   = regexp.MustCompile(`\[\[([^\],\]]+)(?:,[^\]]*)?\]\]|anchor:([\w-]+)\[`)
	adocXref       = regexp.MustCompile(`xref:([^\[\s]+)\[([^\]]*)\]`)
	adocAngleXref  = regexp.MustCompile(`<<([^,>]+)(?:,\s*([^>]+))?>>`)
	adocLink       = regexp.MustCompile(`(?:^|[^:/\w])link:([^\[\s]+)\[([^\]]*)\]`)
	adocURL        = regexp.MustCompile(`(?:^|[\s(<])(https?://[^\s\[<>)]+)(?:\[([^\]]*)\])?`)
	adocImage      = regexp.MustCompile(`image::?([^\[\s]+)\[([^\]]*)\]`)
	adocInclude    = regexp.MustCompile(`^include::([^\[]+)\[[^\]]*\]`)
	adocSource     = regexp.MustCompile(`^\[(?:source)?,\s*([\w+-]+)`)
	adocInlineCode = regexp.MustCompile("`+([^`]+)`+")
	adocAttrRef    = regexp.MustCompile(`\{([A-Za-z0-9_][A-Za-z0-9_-]*)\}`)
)

// AdocID turns a section title into Asciidoctor's default ID: idprefix "_",
// idseparator "_", lowercase, other characters dropped.
func AdocID(title string) string {
	var b strings.Builder
	b.WriteByte('_')
	lastSep := true
	for _, r := range strings.ToLower(adocAttrRef.ReplaceAllString(title, "")) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127:
			b.WriteRune(r)
			lastSep = false
		case r == ' ' || r == '-' || r == '.' || r == '_':
			if !lastSep {
				b.WriteByte('_')
				lastSep = true
			}
		}
	}
	return strings.TrimRight(b.String(), "_")
}

// AsciiDoc parses an AsciiDoc document into the shared page model. It reads
// the document title and attributes, sections with their IDs, xrefs, links,
// images, includes and source blocks. It does not render the document.
func AsciiDoc(src []byte, opt Options) *model.Page {
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	p := &model.Page{Format: "asciidoc", Anchors: map[string]bool{}, Chars: len(text), FrontMatter: map[string]any{}, Raw: src}
	attrs := map[string]string{}
	lines := strings.Split(text, "\n")
	inHeader := true
	pendingID := ""
	section := ""
	var delim string // current delimited block fence
	var blockLang string
	var blockStart int
	var blockBody []string
	seen := map[string]int{}
	var para []string
	paraLine := 0
	flush := func() {
		if len(para) > 0 {
			p.Paragraphs = append(p.Paragraphs, model.Paragraph{Text: strings.Join(para, " "), Line: paraLine, Section: section})
			para = nil
		}
	}
	for i, line := range lines {
		ln := i + 1
		trimmed := strings.TrimSpace(line)
		if delim != "" {
			if trimmed == delim {
				if delim == "----" || delim == "...." {
					p.CodeBlocks = append(p.CodeBlocks, model.CodeBlock{Lang: blockLang, Lines: len(blockBody), Line: blockStart, Content: strings.Join(blockBody, "\n"), Fenced: delim == "----"})
				}
				delim, blockLang, blockBody = "", "", nil
				continue
			}
			if delim == "----" || delim == "...." || delim == "////" || delim == "++++" {
				blockBody = append(blockBody, line)
				continue
			}
		}
		if strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "////") {
			continue
		}
		switch trimmed {
		case "----", "....", "////", "++++":
			flush()
			delim, blockStart = trimmed, ln
			continue
		case "====", "****", "____", "|===", "--":
			flush()
			continue
		}
		if m := adocAttr.FindStringSubmatch(line); m != nil && (inHeader || true) {
			attrs[m[1]] = m[2]
			p.FrontMatter[m[1]] = m[2]
			continue
		}
		if m := adocHeading.FindStringSubmatch(line); m != nil {
			flush()
			lvl := len(m[1])
			title := m[2]
			if lvl == 1 && p.Title == "" {
				p.Title = substitute(title, attrs)
				p.Headings = append(p.Headings, model.Heading{Level: 1, Text: p.Title, Anchor: "", Line: ln})
				inHeader = true
				continue
			}
			inHeader = false
			id := pendingID
			if id == "" {
				base := AdocID(substitute(title, attrs))
				id = base
				if n := seen[base]; n > 0 {
					id = base + "_" + itoa(n+1)
				}
				seen[base]++
			}
			pendingID = ""
			p.Anchors[id] = true
			section = id
			p.Headings = append(p.Headings, model.Heading{Level: lvl, Text: substitute(title, attrs), Anchor: id, Line: ln})
			continue
		}
		if trimmed == "" {
			inHeader = false
			flush()
			continue
		}
		if m := adocBlockID.FindStringSubmatch(trimmed); m != nil {
			pendingID = m[1] + m[2]
			p.Anchors[pendingID] = true
			continue
		}
		if m := adocSource.FindStringSubmatch(trimmed); m != nil {
			blockLang = m[1]
			continue
		}
		if trimmed == "[source]" {
			blockLang = ""
			continue
		}
		if m := adocInclude.FindStringSubmatch(trimmed); m != nil {
			p.Includes = append(p.Includes, model.Include{Target: substitute(m[1], attrs), Line: ln, Kind: "include"})
			continue
		}
		inHeader = false
		for _, m := range adocInlineID.FindAllStringSubmatch(line, -1) {
			p.Anchors[m[1]+m[2]] = true
		}
		add := func(href, txt string, col int, img bool) {
			p.Links = append(p.Links, model.Link{Href: substitute(href, attrs), Text: substitute(txt, attrs), Line: ln, Column: col + 1, Section: section, Image: img})
		}
		for _, m := range adocXref.FindAllStringSubmatchIndex(line, -1) {
			add(AdocXrefHref(line[m[2]:m[3]]), line[m[4]:m[5]], m[0], false)
		}
		for _, m := range adocAngleXref.FindAllStringSubmatchIndex(line, -1) {
			t := line[m[2]:m[3]]
			txt := t
			if m[4] >= 0 {
				txt = line[m[4]:m[5]]
			}
			if strings.Contains(t, ".adoc") {
				add(AdocXrefHref(t), txt, m[0], false)
			} else {
				add("#"+t, txt, m[0], false)
			}
		}
		for _, m := range adocLink.FindAllStringSubmatchIndex(line, -1) {
			add(line[m[2]:m[3]], line[m[4]:m[5]], m[2]-5, false)
		}
		for _, m := range adocURL.FindAllStringSubmatchIndex(line, -1) {
			if m[2] >= 5 && line[m[2]-5:m[2]] == "link:" {
				continue
			}
			txt := ""
			if m[4] >= 0 {
				txt = line[m[4]:m[5]]
			}
			add(line[m[2]:m[3]], txt, m[2], false)
		}
		for _, m := range adocImage.FindAllStringSubmatchIndex(line, -1) {
			alt := strings.SplitN(line[m[4]:m[5]], ",", 2)[0]
			add(line[m[2]:m[3]], alt, m[0], true)
		}
		for _, m := range adocInlineCode.FindAllStringSubmatch(line, -1) {
			p.InlineCode = append(p.InlineCode, model.Span{Text: m[1], Line: ln})
		}
		if len(para) == 0 {
			paraLine = ln
		}
		para = append(para, trimmed)
	}
	flush()
	if t, ok := attrs["description"]; ok {
		p.Description = substitute(t, attrs)
	}
	if a, ok := attrs["page-aliases"]; ok {
		for _, x := range strings.Split(a, ",") {
			p.Aliases = append(p.Aliases, strings.TrimSpace(x))
		}
	}
	p.Words = len(strings.Fields(text))
	return p
}

// AdocXrefHref turns an xref target into a link: page.adoc#id -> page.adoc#id,
// dropping Antora component:module: prefixes.
func AdocXrefHref(t string) string {
	if i := strings.LastIndex(t, ":"); i >= 0 && !strings.Contains(t, "://") {
		t = t[i+1:]
	}
	if i := strings.Index(t, "@"); i >= 0 {
		t = t[i+1:]
	}
	return t
}

func substitute(s string, attrs map[string]string) string {
	return adocAttrRef.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := attrs[m[1:len(m)-1]]; ok {
			return v
		}
		return m
	})
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoa(n/10) + string(rune('0'+n%10))
}
