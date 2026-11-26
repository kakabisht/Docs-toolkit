package convert

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

type renderer struct {
	c         *Converter
	d         *document
	footnotes []string
	fnIDs     map[string]int
	reported  map[string]bool // construct reported once per document
	prot      protector       // shared by nested inline calls, so their placeholders never collide
}

var (
	delimRe     = regexp.MustCompile(`^(-{4,}|\.{4,}|={4,}|\*{4,}|_{4,}|\+{4,}|/{4,}|--|[|!]={3,}|,={3,}|:={3,})\s*$`)
	blockTitle  = regexp.MustCompile(`^\.([^\s.].*)$`)
	admonPara   = regexp.MustCompile(`^(NOTE|TIP|IMPORTANT|WARNING|CAUTION):\s+(.*)$`)
	listItem    = regexp.MustCompile(`^\s*(\*{1,5}|-|\.{1,5}|\d+\.|[a-zA-Z]\.|[ivxIVX]+\))\s+(.*)$`)
	dlistItem   = regexp.MustCompile(`^\s*(.*?\S)(:{2,4}|;;)(?:\s+(.*))?$`)
	calloutItem = regexp.MustCompile(`^<(\d+|\.)>\s+(.*)$`)
	blockMacro  = regexp.MustCompile(`^(image|video|audio|toc)::([^\[]*)\[(.*)\]\s*$`)
	calloutMark = regexp.MustCompile(`(\s*)((?://|#|--|;;?|%|<!--)\s*)?<(\d+|!--\d+--)>(\s*-->)?`)
	checkItem   = regexp.MustCompile(`^\[([ xX*])\]\s+(.*)$`)
)

var admonTypes = map[string]bool{"NOTE": true, "TIP": true, "IMPORTANT": true, "WARNING": true, "CAUTION": true}

func (r *renderer) item(l srcLine, construct, action, msg string) {
	r.c.report.add(Item{File: l.file, Line: l.n, Construct: construct, Action: action, Message: msg})
}

// once reports a construct once per document.
func (r *renderer) once(l srcLine, construct, action, msg string) {
	k := construct + "|" + action + "|" + msg
	if r.reported[k] {
		return
	}
	r.reported[k] = true
	r.item(l, construct, action, msg)
}

func (c *Converter) render(d *document) string {
	r := &renderer{c: c, d: d, fnIDs: map[string]int{}, reported: map[string]bool{}}
	body := r.blocks(d.lines)
	var b strings.Builder
	var fm []string
	if d.title != "" {
		fm = append(fm, "title: "+yamlScalar(stripInline(d.title)))
	}
	if d.author != "" {
		fm = append(fm, "author: "+yamlScalar(d.author))
	}
	for _, kv := range d.front {
		if kv[0] == "tags" {
			var tags []string
			for _, t := range strings.Split(kv[1], ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, yamlScalar(t))
				}
			}
			fm = append(fm, "tags: ["+strings.Join(tags, ", ")+"]")
			continue
		}
		if kv[0] == "author" && d.author != "" {
			continue
		}
		fm = append(fm, kv[0]+": "+yamlScalar(kv[1]))
	}
	if len(fm) > 0 {
		b.WriteString("---\n" + strings.Join(fm, "\n") + "\n---\n\n")
	}
	if c.opt.H1 && d.title != "" {
		b.WriteString("# " + r.inline(d.title, srcLine{"", d.file, d.titleLine}) + "\n\n")
	}
	b.WriteString(body)
	if len(r.footnotes) > 0 {
		b.WriteString("\n")
		for i, f := range r.footnotes {
			fmt.Fprintf(&b, "\n[^%d]: %s", i+1, f)
		}
		b.WriteString("\n")
	}
	out := collapseBlank(strings.TrimRight(b.String(), "\n") + "\n")
	c.contentCheck(d, out)
	return out
}

var blankRuns = regexp.MustCompile(`\n{3,}`)

func collapseBlank(s string) string {
	// keep fenced code as it is
	var b strings.Builder
	parts := strings.Split(s, "\n```")
	for i, p := range parts {
		if i%2 == 0 {
			p = blankRuns.ReplaceAllString(p, "\n\n")
		}
		if i > 0 {
			b.WriteString("\n```")
		}
		b.WriteString(p)
	}
	return b.String()
}

func yamlScalar(s string) string {
	b, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Sprintf("%q", s)
	}
	return strings.TrimSuffix(string(b), "\n")
}

type pending struct {
	attrs  string // inside the brackets
	title  string
	anchor string
	line   srcLine
}

func (p *pending) style() string {
	if p.attrs == "" {
		return ""
	}
	first := strings.TrimSpace(splitAttrs(p.attrs)[0])
	if i := strings.IndexAny(first, "#.%"); i >= 0 {
		first = first[:i]
	}
	return first
}

func (p *pending) positional(n int) string {
	parts := splitAttrs(p.attrs)
	k := 0
	for _, x := range parts {
		if strings.Contains(x, "=") {
			continue
		}
		if k == n {
			return strings.Trim(strings.TrimSpace(x), `"`)
		}
		k++
	}
	return ""
}

func (p *pending) named(name string) string { return parseAttrList(p.attrs)[name] }

func (p *pending) option(o string) bool {
	return strings.Contains(p.attrs, "%"+o) || strings.Contains(p.named("options"), o) || strings.Contains(p.named("opts"), o)
}

// blocks renders a sequence of lines.
func (r *renderer) blocks(lines []srcLine) string {
	var parts []string
	var pend pending
	add := func(s string) {
		if pend.anchor != "" {
			s = fmt.Sprintf(`<a id="%s"></a>`, pend.anchor) + "\n\n" + s
		}
		parts = append(parts, s)
		pend = pending{}
	}
	i := 0
	for i < len(lines) {
		l := lines[i]
		t := l.text
		switch {
		case strings.TrimSpace(t) == "":
			i++
		case strings.HasPrefix(t, "//") && !strings.HasPrefix(t, "////"):
			if txt := strings.TrimSpace(strings.TrimPrefix(t, "//")); txt != "" && !tagMarker.MatchString(t) {
				parts = append(parts, "<!-- "+htmlComment(txt)+" -->")
			}
			i++
		case blockAnchorRe.MatchString(t):
			pend.anchor = blockAnchorRe.FindStringSubmatch(t)[1]
			pend.line = l
			i++
		case blockAttrs.MatchString(t) && !strings.HasPrefix(t, "[["):
			inner := strings.TrimSpace(t[1 : len(t)-1])
			if m := idAttrRe.FindStringSubmatch(t); m != nil {
				pend.anchor = m[1]
			} else if m := idNamedRe.FindStringSubmatch(t); m != nil {
				pend.anchor = m[1]
			}
			if pend.attrs != "" {
				pend.attrs += "," + inner
			} else {
				pend.attrs = inner
			}
			pend.line = l
			i++
		case blockTitle.MatchString(t) && !strings.HasPrefix(t, ".."):
			pend.title = blockTitle.FindStringSubmatch(t)[1]
			pend.line = l
			i++
		case sectionRe.MatchString(t):
			m := sectionRe.FindStringSubmatch(t)
			lvl := len(m[1])
			s := strings.Repeat("#", lvl) + " " + r.inline(m[2], l)
			if pend.anchor != "" {
				s += " {#" + pend.anchor + "}"
				r.once(l, "explicit id", Approximated, "explicit section ids are written as {#id}; the site needs heading attributes (MkDocs attr_list, Hugo's default Markdown, Docusaurus)")
				pend.anchor = ""
			}
			if st := pend.style(); st == "discrete" || st == "float" {
				r.item(l, "discrete heading", Approximated, "discrete headings become ordinary headings and appear in the table of contents")
			}
			if lvl == 1 && r.d.title != "" {
				if r.d.attrs["doctype"] == "book" {
					r.once(l, "book part", Approximated, "book part titles (extra level-0 titles) become level-1 headings")
				} else {
					r.item(l, "heading", Review, "a second level-0 title outside a book: written as a level-1 heading")
				}
			}
			add(s)
			i++
		case delimRe.MatchString(t):
			end := i + 1
			for end < len(lines) && lines[end].text != t {
				end++
			}
			if end >= len(lines) {
				r.item(l, "block", Review, fmt.Sprintf("block delimiter %q is never closed; the rest of the file is treated as its content", t))
			}
			inner := lines[i+1 : min(end, len(lines))]
			add(r.delimited(t, inner, &pend, l))
			i = end + 1
		case admonPara.MatchString(t):
			m := admonPara.FindStringSubmatch(t)
			j := i + 1
			for j < len(lines) && strings.TrimSpace(lines[j].text) != "" && !delimRe.MatchString(lines[j].text) {
				j++
			}
			body := append([]srcLine{{m[2], l.file, l.n}}, lines[i+1:j]...)
			add(r.admonition(m[1], pend.title, r.paragraph(body, &pending{}), l))
			i = j
		case blockMacro.MatchString(t):
			add(r.macro(blockMacro.FindStringSubmatch(t), &pend, l))
			i++
		case t == "'''" || t == "---" || t == "***" || t == "- - -" || t == "* * *":
			add("---")
			i++
		case t == "<<<":
			r.once(l, "page break", Dropped, "page breaks have no Markdown equivalent")
			i++
		case calloutItem.MatchString(t):
			j := i
			var items []string
			for j < len(lines) {
				if m := calloutItem.FindStringSubmatch(lines[j].text); m != nil {
					n := m[1]
					if n == "." {
						n = fmt.Sprint(len(items) + 1)
					}
					// a bullet list keeps it from merging with a numbered list next to it
					items = append(items, fmt.Sprintf("- (%s) %s", n, r.inline(m[2], lines[j])))
					j++
					continue
				}
				if strings.TrimSpace(lines[j].text) != "" && len(items) > 0 && !delimRe.MatchString(lines[j].text) && !listItem.MatchString(lines[j].text) {
					items[len(items)-1] += " " + r.inline(strings.TrimSpace(lines[j].text), lines[j])
					j++
					continue
				}
				break
			}
			s := strings.Join(items, "\n")
			if pend.title != "" {
				s = "**" + r.inline(pend.title, pend.line) + "**\n\n" + s
			}
			add(s)
			i = j
		case listItem.MatchString(t) || (isDlist(t) && pend.style() != "source"):
			s, j := r.list(lines, i)
			if pend.title != "" {
				s = "**" + r.inline(pend.title, pend.line) + "**\n\n" + s
			}
			add(s)
			i = j
		case (strings.HasPrefix(t, " ") || strings.HasPrefix(t, "\t")) && pend.style() == "":
			j := i
			var code []string
			for j < len(lines) && strings.TrimSpace(lines[j].text) != "" {
				code = append(code, lines[j].text)
				j++
			}
			s := fence("", dedentStrings(code))
			if pend.title != "" {
				s = "**" + r.inline(pend.title, pend.line) + "**\n\n" + s
			}
			add(s)
			i = j
		default:
			j := i
			for j < len(lines) && strings.TrimSpace(lines[j].text) != "" && (j == i || !delimRe.MatchString(lines[j].text) && !blockAttrs.MatchString(lines[j].text) && !blockAnchorRe.MatchString(lines[j].text)) {
				j++
			}
			add(r.styledParagraph(lines[i:j], &pend))
			i = j
		}
	}
	return strings.Join(parts, "\n\n")
}

func isDlist(t string) bool {
	m := dlistItem.FindStringSubmatch(t)
	if m == nil || strings.Contains(t, "://") && !strings.Contains(strings.SplitN(t, "://", 2)[0], "::") {
		return false
	}
	// "foo::bar[]" macros and C++ scopes are not description lists
	if strings.HasSuffix(m[1], "]") || strings.Contains(m[1], "::") {
		return false
	}
	return m[3] != "" || strings.HasSuffix(strings.TrimSpace(t), m[2])
}

func htmlComment(s string) string { return strings.ReplaceAll(s, "--", "- -") }

func dedentStrings(in []string) []string {
	ls := make([]srcLine, len(in))
	for i, s := range in {
		ls[i] = srcLine{text: s}
	}
	out := dedent(ls)
	res := make([]string, len(out))
	for i, l := range out {
		res[i] = l.text
	}
	return res
}

func fence(lang string, code []string) string {
	f := "```"
	for _, c := range code {
		for strings.Contains(c, f) {
			f += "`"
		}
	}
	return f + lang + "\n" + strings.Join(code, "\n") + "\n" + f
}

// paragraph converts the lines of one paragraph, keeping its line breaks.
func (r *renderer) paragraph(lines []srcLine, p *pending) string {
	hard := p.option("hardbreaks") || r.d.attrs["hardbreaks"] != "" || hasAttr(r.d.attrs, "hardbreaks-option")
	var out []string
	for k, l := range lines {
		t := strings.TrimSpace(l.text)
		brk := false
		if strings.HasSuffix(t, " +") {
			t = strings.TrimSuffix(t, " +")
			brk = true
		}
		s := escapeLineStart(r.inline(t, l))
		if (brk || hard) && k < len(lines)-1 {
			s += `\`
		}
		out = append(out, s)
	}
	return strings.Join(out, "\n")
}

func hasAttr(m map[string]string, k string) bool { _, ok := m[k]; return ok }

var mdLineStart = regexp.MustCompile(`^(#{1,6}\s|>|[-+*]\s|\d+[.)]\s|={3,}\s*$|-{3,}\s*$)`)

// escapeLineStart stops prose from turning into Markdown structure.
func escapeLineStart(s string) string {
	if m := mdLineStart.FindStringIndex(s); m != nil {
		if d := regexp.MustCompile(`^\d+`).FindString(s); d != "" {
			return d + `\` + s[len(d):]
		}
		return `\` + s
	}
	return s
}

func (r *renderer) styledParagraph(lines []srcLine, p *pending) string {
	l := lines[0]
	title := ""
	if p.title != "" {
		title = "**" + r.inline(p.title, p.line) + "**\n\n"
	}
	switch st := p.style(); {
	case admonTypes[st]:
		return r.admonition(st, p.title, r.paragraph(lines, p), l)
	case st == "source" || st == "listing" || st == "literal" || (st == "" && p.positional(1) != "" && strings.HasPrefix(p.attrs, ",")):
		var code []string
		for _, x := range lines {
			code = append(code, x.text)
		}
		return title + r.code(p, code, l)
	case st == "quote" || st == "verse":
		return r.quote(p, r.paragraph(lines, &pending{attrs: map[bool]string{true: "%hardbreaks", false: ""}[st == "verse"]}), l)
	case st == "pass":
		var raw []string
		for _, x := range lines {
			raw = append(raw, x.text)
		}
		r.item(l, "passthrough", Review, "passthrough content kept as raw HTML or text")
		return strings.Join(raw, "\n")
	case st == "abstract" || st == "partintro" || st == "normal" || st == "":
		if strings.HasPrefix(st, ".") || strings.Contains(p.attrs, ".") && st == "" && !strings.Contains(p.attrs, "=") {
			r.once(l, "role", Dropped, fmt.Sprintf("paragraph role [%s] has no Markdown equivalent", p.attrs))
		}
		return title + r.paragraph(lines, p)
	default:
		r.item(l, "paragraph style", Approximated, fmt.Sprintf("paragraph style [%s] rendered as a plain paragraph", st))
		return title + r.paragraph(lines, p)
	}
}

func (r *renderer) quote(p *pending, body string, l srcLine) string {
	s := prefixLines(body, "> ")
	who, cite := p.positional(1), p.positional(2)
	if who != "" || cite != "" {
		a := "— " + strings.TrimSpace(strings.Join([]string{who, cite}, ", "))
		s += "\n>\n> " + strings.TrimSuffix(a, ", ")
	}
	if p.style() == "verse" {
		r.once(l, "verse", Approximated, "verse blocks become block quotes with line breaks")
	}
	return s
}

func prefixLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = strings.TrimRight(prefix, " ")
		} else {
			lines[i] = prefix + l
		}
	}
	return strings.Join(lines, "\n")
}

func indent(s string, n int) string {
	pad := strings.Repeat(" ", n)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

var docusaurusAdmon = map[string]string{"NOTE": "note", "TIP": "tip", "IMPORTANT": "info", "WARNING": "warning", "CAUTION": "danger"}

func (r *renderer) admonition(kind, title, body string, l srcLine) string {
	low := strings.ToLower(kind)
	switch r.c.opt.Target {
	case TargetMkDocs:
		head := "!!! " + low
		if title != "" {
			head += ` "` + strings.ReplaceAll(r.inline(title, l), `"`, `'`) + `"`
		}
		return head + "\n\n" + indent(body, 4)
	case TargetDocusaurus:
		t := docusaurusAdmon[kind]
		if t != low {
			r.once(l, "admonition", Approximated, fmt.Sprintf("%s admonitions become :::%s (Docusaurus has no %s)", kind, t, low))
		}
		head := ":::" + t
		if title != "" {
			head += "[" + r.inline(title, l) + "]"
		}
		return head + "\n\n" + body + "\n\n:::"
	case TargetPlain:
		label := strings.ToUpper(low[:1]) + low[1:]
		if title != "" {
			label += ": " + r.inline(title, l)
		}
		r.once(l, "admonition", Approximated, "admonitions become block quotes with a bold label")
		return prefixLines("**"+label+":** "+body, "> ")
	case TargetHugo:
		// Hugo (0.134 and later) passes the text after the marker to the render hook as AlertTitle
		s := "> [!" + kind + "]"
		if title != "" {
			s += " " + r.inline(title, l)
		}
		r.once(l, "admonition", Approximated, "admonitions become blockquote alerts; the site needs a blockquote render hook for them to look like admonitions")
		return s + "\n" + prefixLines(body, "> ")
	default: // gfm
		s := "> [!" + kind + "]\n"
		if title != "" {
			s += "> **" + r.inline(title, l) + "**\n>\n"
			r.once(l, "admonition title", Approximated, "admonition titles become a bold first line (GitHub alerts have no titles)")
		}
		return s + prefixLines(body, "> ")
	}
}

// delimited renders a delimited block.
func (r *renderer) delimited(delim string, inner []srcLine, p *pending, l srcLine) string {
	var raw []string
	for _, x := range inner {
		raw = append(raw, x.text)
	}
	title := ""
	if p.title != "" {
		title = "**" + r.inline(p.title, p.line) + "**\n\n"
	}
	st := p.style()
	switch {
	case strings.HasPrefix(delim, "----"):
		if st == "" || st == "source" || st == "listing" || p.positional(1) != "" {
			if p.title != "" {
				r.once(l, "block title", Approximated, "code block titles become a bold line above the block")
			}
			return title + r.code(p, raw, l)
		}
		if admonTypes[st] {
			return r.admonition(st, p.title, fence("", raw), l)
		}
		return title + fence("", raw)
	case strings.HasPrefix(delim, "...."):
		return title + fence("", raw)
	case strings.HasPrefix(delim, "++++"):
		r.item(l, "passthrough", Review, "passthrough block kept as raw HTML; check it renders on the target site")
		return strings.Join(raw, "\n")
	case strings.HasPrefix(delim, "////"):
		if len(raw) == 0 {
			return ""
		}
		return "<!--\n" + htmlComment(strings.Join(raw, "\n")) + "\n-->"
	case strings.HasPrefix(delim, "|===") || strings.HasPrefix(delim, "!===") || strings.HasPrefix(delim, ",===") || strings.HasPrefix(delim, ":==="):
		return r.table(delim, inner, p, l)
	}
	body := r.blocks(inner)
	switch {
	case admonTypes[st]:
		return r.admonition(st, p.title, body, l)
	case st == "quote" || st == "verse" || strings.HasPrefix(delim, "____"):
		if st == "verse" {
			body = r.paragraph(inner, &pending{attrs: "%hardbreaks"})
		}
		return title + r.quote(p, body, l)
	case st == "source" || st == "listing" || st == "literal":
		return title + r.code(p, raw, l)
	case strings.HasPrefix(delim, "===="):
		if r.c.opt.Target == TargetMkDocs {
			head := "??? example"
			if p.option("collapsible") || p.named("opts") == "collapsible" {
				head = "??? example"
			} else {
				head = "!!! example"
			}
			if p.title != "" {
				head += ` "` + r.inline(p.title, p.line) + `"`
			}
			return head + "\n\n" + indent(body, 4)
		}
		if p.option("collapsible") {
			r.once(l, "collapsible block", Approximated, "collapsible blocks become <details>")
			return "<details>\n<summary>" + r.inline(firstNonEmptyStr(p.title, "Details"), l) + "</summary>\n\n" + body + "\n\n</details>"
		}
		r.once(l, "example block", Approximated, "example blocks lose their frame: the content is kept with its title in bold")
		return title + body
	case strings.HasPrefix(delim, "****"):
		r.once(l, "sidebar", Approximated, "sidebars become block quotes")
		return prefixLines(strings.TrimSuffix(title, "\n\n")+map[bool]string{true: "\n\n", false: ""}[title != ""]+body, "> ")
	default: // open block --
		return title + body
	}
}

func firstNonEmptyStr(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

var calloutEnd = regexp.MustCompile(`(<\d+>|<!--\d+-->)(\s*-->)?\s*$`)

// commentFor gives the line comment for a code language, or "" when unknown.
func commentFor(lang string) string {
	switch strings.ToLower(lang) {
	case "bash", "sh", "shell", "console", "zsh", "fish", "python", "py", "ruby", "rb", "yaml", "yml", "toml", "perl", "r", "powershell", "ps1", "dockerfile", "make", "makefile", "ini", "conf", "terraform", "hcl", "properties", "nix", "elixir", "crystal", "julia":
		return "#"
	case "go", "golang", "java", "javascript", "js", "typescript", "ts", "jsx", "tsx", "c", "cpp", "c++", "csharp", "cs", "rust", "rs", "kotlin", "kt", "swift", "scala", "groovy", "php", "dart", "json5", "jsonc", "proto", "protobuf", "zig", "sass", "scss", "less", "graphql":
		return "//"
	case "sql", "lua", "haskell", "hs", "ada", "elm":
		return "--"
	case "xml", "html", "svg", "markdown", "md", "vue":
		return "<!--"
	case "lisp", "clojure", "scheme", "elisp":
		return ";;"
	case "tex", "latex", "erlang", "matlab":
		return "%"
	}
	return ""
}

// code renders a source or listing block; callout markers become (n).
func (r *renderer) code(p *pending, raw []string, l srcLine) string {
	lang := p.positional(1)
	if lang == "" && p.style() == "source" {
		lang = r.d.attrs["source-language"]
	}
	if p.option("linenums") || p.positional(2) == "linenums" {
		r.once(l, "line numbers", Dropped, "line numbering on code blocks is not carried over")
	}
	out := make([]string, len(raw))
	callouts, bare := false, false
	comment := commentFor(lang)
	for k, line := range raw {
		if !calloutEnd.MatchString(line) {
			out[k] = line
			continue
		}
		out[k] = calloutMark.ReplaceAllStringFunc(line, func(m string) string {
			callouts = true
			sm := calloutMark.FindStringSubmatch(m)
			n := strings.Trim(sm[3], "!-")
			if sm[2] != "" {
				c := strings.TrimSpace(sm[2])
				if c == "<!--" {
					return sm[1] + "<!-- (" + n + ") -->"
				}
				return sm[1] + c + " (" + n + ")"
			}
			switch comment {
			case "":
				bare = true
				return ""
			case "<!--":
				return " <!-- (" + n + ") -->"
			}
			return " " + comment + " (" + n + ")"
		})
		out[k] = strings.TrimRight(out[k], " ")
	}
	if callouts {
		r.once(l, "callouts", Approximated, "callout markers become (n) in code comments, and the callout list becomes a bullet list of (n) items")
	}
	if bare {
		r.once(l, "callouts", Review, "callout markers removed: the code language has no known comment syntax, so the list no longer points into the code")
	}
	return fence(lang, out)
}

// macro renders a block macro: image, video, audio, toc.
func (r *renderer) macro(m []string, p *pending, l srcLine) string {
	name, target, attrs := m[1], m[2], m[3]
	switch name {
	case "toc":
		r.item(l, "toc", Dropped, "toc::[] dropped: the site generates its own table of contents")
		return ""
	case "image":
		parts := splitAttrs(attrs)
		alt := strings.Trim(strings.TrimSpace(parts[0]), `"`)
		if alt == "" || strings.Contains(alt, "=") {
			alt = parseAttrList(attrs)["alt"]
		}
		if alt == "" {
			alt = strings.TrimSuffix(path.Base(target), path.Ext(target))
			r.item(l, "image", Review, fmt.Sprintf("image %s has no alt text; the file name was used", target))
		}
		src := r.imagePath(target, l)
		if len(parts) > 1 || parseAttrList(attrs)["width"] != "" {
			r.once(l, "image size", Dropped, "image width, height and alignment are not carried over")
		}
		s := fmt.Sprintf("![%s](%s)", escapeBrackets(alt), mdURL(src))
		if lk := parseAttrList(attrs)["link"]; lk != "" {
			s = fmt.Sprintf("[%s](%s)", s, mdURL(lk))
		}
		if p.title != "" {
			r.once(l, "image caption", Approximated, "image titles become an italic caption line")
			s += "\n\n_" + r.inline(p.title, p.line) + "_"
		}
		return s
	default:
		r.item(l, name, Approximated, fmt.Sprintf("%s:: becomes a link", name))
		u := target
		if name == "video" && parseAttrList(attrs)["youtube"] != "" || strings.Contains(attrs, "youtube") {
			u = "https://www.youtube.com/watch?v=" + target
		}
		return fmt.Sprintf("[%s](%s)", firstNonEmptyStr(r.inline(p.title, l), strings.Title(name)+": "+target), mdURL(u))
	}
}

func escapeBrackets(s string) string { return strings.NewReplacer("[", `\[`, "]", `\]`).Replace(s) }

func mdURL(u string) string {
	if strings.ContainsAny(u, " ()") {
		return "<" + u + ">"
	}
	return u
}

// imagePath applies imagesdir (or the Antora images folder) and records the
// file so a batch run can copy it next to the Markdown.
func (r *renderer) imagePath(target string, l srcLine) string {
	if strings.Contains(target, "://") || strings.HasPrefix(target, "/") || strings.HasPrefix(target, "data:") {
		return target
	}
	if i := strings.Index(target, "$"); i > 0 {
		target = target[i+1:]
	}
	p := target
	if dir := r.d.attrs["imagesdir"]; dir != "" && !strings.Contains(dir, "://") {
		p = path.Join(dir, target)
	} else if dir != "" {
		return strings.TrimSuffix(dir, "/") + "/" + target
	} else if mod := antoraModule(r.d.file); mod != "" {
		// Antora: images live in the module's images (or assets/images) folder
		for _, f := range []string{"images", "assets/images"} {
			cand := path.Join(mod, f, target)
			if _, err := os.Stat(filepath.Join(r.c.root, filepath.FromSlash(cand))); err == nil {
				rel, _ := filepath.Rel(filepath.Dir(filepath.FromSlash(r.d.file)), filepath.FromSlash(cand))
				p = filepath.ToSlash(rel)
				r.once(l, "image path", Approximated, "Antora image references are rewritten to paths relative to the page")
				break
			}
		}
	}
	full := path.Join(path.Dir(r.d.file), p)
	if _, err := os.Stat(filepath.Join(r.c.root, filepath.FromSlash(full))); err == nil {
		if !containsStr(r.d.assets, p) {
			r.d.assets = append(r.d.assets, p)
			sort.Strings(r.d.assets)
		}
	}
	return p
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// antoraModule returns the module folder (…/modules/NAME) a file is in, for
// pages, partials and nav files alike, or "" outside an Antora layout.
func antoraModule(file string) string {
	d := path.Dir(file)
	for d != "." && d != "/" && d != "" {
		if path.Base(path.Dir(d)) == "modules" {
			return d
		}
		if path.Base(d) == "pages" {
			return path.Dir(d)
		}
		d = path.Dir(d)
	}
	return ""
}
