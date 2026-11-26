package convert

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type srcLine struct {
	text string
	file string
	n    int
}

type document struct {
	file      string
	lines     []srcLine
	title     string
	titleLine int
	author    string
	front     [][2]string // front matter from header attributes, in order
	includes  []string    // files this one includes (relative to the root)
	assets    []string    // local images it uses, relative to its folder
	attrs     map[string]string
}

var builtinAttrs = map[string]string{
	"empty": "", "sp": " ", "nbsp": " ", "zwsp": "​", "wj": "⁠", "apos": "'", "quot": `"`,
	"lsquo": "‘", "rsquo": "’", "ldquo": "“", "rdquo": "”", "deg": "°", "plus": "+", "brvbar": "¦", "vbar": "|",
	"amp": "&", "lt": "<", "gt": ">", "startsb": "[", "endsb": "]", "caret": "^", "asterisk": "*", "tilde": "~",
	"backslash": `\`, "backtick": "`", "two-colons": "::", "two-semicolons": ";;", "cpp": "C++", "pp": "++",
}

var (
	attrEntry   = regexp.MustCompile(`^:(!?)([A-Za-z0-9_][A-Za-z0-9_-]*)(!?):(?:\s+(.*?))?\s*$`)
	condRe      = regexp.MustCompile(`^(ifdef|ifndef|ifeval|endif)::([^\[]*)\[(.*)\]\s*$`)
	includeRe   = regexp.MustCompile(`^include::([^\[]+)\[(.*)\]\s*$`)
	attrRef     = regexp.MustCompile(`\\?\{([A-Za-z0-9_][A-Za-z0-9_-]*)\}`)
	headingLine = regexp.MustCompile(`^(=+)(\s+\S.*)$`)
	tagMarker   = regexp.MustCompile(`\b(tag|end)::([^\[\s]+)\[\]`)
	verbatimDel = regexp.MustCompile(`^(-{4,}|\.{4,}|\+{4,}|/{4,})\s*$`)
	blockAttrs  = regexp.MustCompile(`^\[[^\]]*\]\s*$`)
)

type cond struct {
	skip    bool // this level is not emitted
	parent  bool // the enclosing level was already skipping
	unknown bool
	start   srcLine
	text    string
}

type pre struct {
	c      *Converter
	doc    *document
	attrs  map[string]string
	stack  []cond
	offset int // heading level offset from includes and :leveloffset:
	depth  int
}

// load reads a document: header, attributes, conditionals and includes are
// resolved here, like Asciidoctor's preprocessor.
func (c *Converter) load(file, src string) *document {
	d := &document{file: file}
	p := &pre{c: c, doc: d, attrs: map[string]string{}}
	for k, v := range c.componentAttrs(file) {
		p.attrs[k] = v
	}
	for k, v := range c.opt.Attributes {
		p.attrs[k] = v
	}
	if _, ok := p.attrs["idprefix"]; !ok {
		p.attrs["idprefix"] = "_"
	}
	if _, ok := p.attrs["idseparator"]; !ok {
		p.attrs["idseparator"] = "_"
	}
	lines := splitLines(src, file)
	lines = p.header(lines)
	p.lines(lines, 0)
	for _, s := range p.stack {
		p.item(s.start, "conditional", Review, fmt.Sprintf("%s has no matching endif::[]", s.text))
	}
	d.attrs = p.attrs
	return d
}

func splitLines(src, file string) []srcLine {
	src = strings.TrimPrefix(strings.ReplaceAll(src, "\r\n", "\n"), "\ufeff")
	raw := strings.Split(src, "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	out := make([]srcLine, len(raw))
	for i, l := range raw {
		out[i] = srcLine{strings.TrimRight(l, " \t"), file, i + 1}
	}
	return out
}

func (p *pre) item(l srcLine, construct, action, msg string) {
	p.c.report.add(Item{File: l.file, Line: l.n, Construct: construct, Action: action, Message: msg})
}

// header takes the document title, author line and header attributes.
func (p *pre) header(lines []srcLine) []srcLine {
	i := 0
	for i < len(lines) && (lines[i].text == "" || strings.HasPrefix(lines[i].text, "//") && !strings.HasPrefix(lines[i].text, "////")) {
		i++
	}
	// attribute entries may come before the title too
	for i < len(lines) && attrEntry.MatchString(lines[i].text) {
		p.entry(lines[i], true)
		i++
	}
	if i >= len(lines) || !strings.HasPrefix(lines[i].text, "= ") {
		return lines[i:]
	}
	p.doc.title = p.subst(strings.TrimSpace(lines[i].text[2:]), lines[i])
	p.doc.titleLine = lines[i].n
	i++
	// author and revision lines
	for k := 0; k < 2 && i < len(lines) && lines[i].text != "" && !strings.HasPrefix(lines[i].text, ":") && !strings.HasPrefix(lines[i].text, "//"); k++ {
		if k == 0 {
			p.doc.author = lines[i].text
			p.item(lines[i], "author line", Approximated, "moved to the front matter as author")
		} else {
			p.item(lines[i], "revision line", Dropped, fmt.Sprintf("revision line %q is not carried over", lines[i].text))
		}
		i++
	}
	for i < len(lines) && lines[i].text != "" {
		l := lines[i]
		switch {
		case attrEntry.MatchString(l.text):
			p.entry(l, true)
		case strings.HasPrefix(l.text, "//"):
		case condRe.MatchString(l.text):
			// conditionals in the header are handled with the body
			return lines[i:]
		default:
			return lines[i:]
		}
		i++
	}
	return lines[i:]
}

var frontKeys = map[string]string{"description": "description", "keywords": "tags", "author": "author"}

// entry records an attribute entry; header entries can become front matter.
func (p *pre) entry(l srcLine, inHeader bool) {
	m := attrEntry.FindStringSubmatch(l.text)
	name := m[2]
	unset := m[1] == "!" || m[3] == "!"
	val := p.subst(m[4], l)
	if _, fixed := p.c.opt.Attributes[name]; fixed {
		return // values given on the command line win, as with asciidoctor -a
	}
	if unset {
		delete(p.attrs, name)
		return
	}
	if name == "leveloffset" {
		p.offset = levelOffset(val, p.offset)
		return
	}
	p.attrs[name] = val
	if !inHeader {
		return
	}
	if k, ok := frontKeys[name]; ok {
		p.doc.front = append(p.doc.front, [2]string{k, val})
	} else if strings.HasPrefix(name, "page-") {
		p.doc.front = append(p.doc.front, [2]string{strings.TrimPrefix(name, "page-"), val})
	}
}

func levelOffset(v string, cur int) int {
	v = strings.TrimSpace(v)
	n, err := strconv.Atoi(strings.TrimPrefix(v, "+"))
	if err != nil {
		return cur
	}
	if strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") {
		return cur + n
	}
	return n
}

// subst replaces attribute references in text. Unknown references are left
// as written and reported.
func (p *pre) subst(text string, l srcLine) string {
	if !strings.Contains(text, "{") {
		return text
	}
	return attrRef.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasPrefix(m, `\`) {
			return m[1:]
		}
		name := m[1 : len(m)-1]
		if v, ok := p.attrs[name]; ok {
			return v
		}
		if v, ok := builtinAttrs[name]; ok {
			return v
		}
		p.item(l, "attribute", Review, fmt.Sprintf("attribute {%s} is not defined; left as written", name))
		return m
	})
}

func (p *pre) skipping() bool {
	return len(p.stack) > 0 && (p.stack[len(p.stack)-1].skip || p.stack[len(p.stack)-1].parent)
}

func (p *pre) emit(l srcLine) { p.doc.lines = append(p.doc.lines, l) }

// lines processes body lines: conditionals, includes, attribute entries,
// and attribute references outside verbatim blocks.
func (p *pre) lines(lines []srcLine, offset int) {
	verbatim := ""
	subsAttrs := false
	lastAttrs := ""
	for _, l := range lines {
		t := l.text
		if m := condRe.FindStringSubmatch(t); m != nil {
			p.conditional(l, m)
			continue
		}
		if p.skipping() {
			continue
		}
		if m := includeRe.FindStringSubmatch(t); m != nil {
			p.include(l, m[1], m[2], offset)
			continue
		}
		if verbatim != "" {
			if t == verbatim {
				verbatim = ""
			} else if subsAttrs {
				l.text = p.subst(t, l)
			}
			p.emit(l)
			continue
		}
		if verbatimDel.MatchString(t) {
			verbatim = t
			subsAttrs = strings.Contains(lastAttrs, "attributes")
			p.emit(l)
			continue
		}
		if attrEntry.MatchString(t) {
			p.entry(l, false)
			continue
		}
		if blockAttrs.MatchString(t) {
			lastAttrs = t
		} else if t != "" {
			lastAttrs = ""
		}
		if m := headingLine.FindStringSubmatch(t); m != nil && (offset+p.offset) != 0 {
			lvl := max(1, len(m[1])+offset+p.offset)
			t = strings.Repeat("=", lvl) + m[2]
		}
		l.text = p.subst(t, l)
		p.emit(l)
	}
}

func (p *pre) conditional(l srcLine, m []string) {
	kind, target, content := m[1], strings.TrimSpace(m[2]), m[3]
	if kind == "endif" {
		if len(p.stack) == 0 {
			p.item(l, "conditional", Review, "endif::[] without an opening conditional")
			return
		}
		top := p.stack[len(p.stack)-1]
		p.stack = p.stack[:len(p.stack)-1]
		if top.parent {
			return
		}
		span := fmt.Sprintf("lines %d-%d", top.start.n, l.n)
		switch {
		case top.unknown && top.skip:
			p.item(top.start, "conditional", Review, fmt.Sprintf("%s could not be evaluated; %s left out (pass the attribute with --attr)", top.text, span))
		case top.unknown:
			p.item(top.start, "conditional", Review, fmt.Sprintf("%s could not be evaluated; %s kept (pass the attribute with --attr)", top.text, span))
		case top.skip:
			p.item(top.start, "conditional", Dropped, fmt.Sprintf("%s is false; %s left out", top.text, span))
		default:
			p.item(top.start, "conditional", Approximated, fmt.Sprintf("%s is true; %s kept without the condition", top.text, span))
		}
		return
	}
	text := fmt.Sprintf("%s::%s[%s]", kind, target, content)
	ok, known := p.eval(kind, target, content, l)
	if kind != "ifeval" && strings.TrimSpace(content) != "" {
		// single-line form: ifdef::attr[text]
		if p.skipping() {
			return
		}
		if ok {
			l.text = p.subst(content, l)
			p.emit(l)
			p.item(l, "conditional", Approximated, fmt.Sprintf("%s is true; text kept without the condition", text))
		} else {
			p.item(l, "conditional", Dropped, fmt.Sprintf("%s is false; text left out", text))
		}
		return
	}
	skip := !ok
	if !known {
		skip = p.c.opt.Unknown == "drop"
	}
	p.stack = append(p.stack, cond{skip: skip, parent: p.skipping(), unknown: !known, start: l, text: text})
}

var evalRe = regexp.MustCompile(`^(.+?)\s*(==|!=|<=|>=|<|>)\s*(.+)$`)

// eval returns the condition's value and whether it could be worked out.
func (p *pre) eval(kind, target, content string, l srcLine) (bool, bool) {
	defined := func(n string) bool { _, ok := p.attrs[n]; return ok }
	switch kind {
	case "ifdef", "ifndef":
		var v bool
		switch {
		case strings.Contains(target, "+"):
			v = true
			for _, n := range strings.Split(target, "+") {
				v = v && defined(strings.TrimSpace(n))
			}
		default:
			for _, n := range strings.Split(target, ",") {
				v = v || defined(strings.TrimSpace(n))
			}
		}
		if kind == "ifndef" {
			v = !v
		}
		return v, true
	case "ifeval":
		m := evalRe.FindStringSubmatch(strings.TrimSpace(content))
		if m == nil {
			return true, false
		}
		val := func(s string) (string, float64, bool, bool) {
			s = strings.TrimSpace(s)
			s = attrRef.ReplaceAllStringFunc(s, func(r string) string {
				if v, ok := p.attrs[r[1:len(r)-1]]; ok {
					return v
				}
				return r
			})
			if strings.Contains(s, "{") {
				return "", 0, false, false
			}
			if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
				return s[1 : len(s)-1], 0, false, true
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return s, f, true, true
			}
			return s, 0, false, true
		}
		ls, lf, lnum, ok1 := val(m[1])
		rs, rf, rnum, ok2 := val(m[3])
		if !ok1 || !ok2 {
			return true, false
		}
		cmp := strings.Compare(ls, rs)
		if lnum && rnum {
			switch {
			case lf < rf:
				cmp = -1
			case lf > rf:
				cmp = 1
			default:
				cmp = 0
			}
		}
		switch m[2] {
		case "==":
			return cmp == 0, true
		case "!=":
			return cmp != 0, true
		case "<":
			return cmp < 0, true
		case "<=":
			return cmp <= 0, true
		case ">":
			return cmp > 0, true
		case ">=":
			return cmp >= 0, true
		}
	}
	return true, false
}

// resolve finds an include target: a path relative to the including file, or
// an Antora resource ID (partial$x.adoc, module:example$y.yaml).
func (p *pre) resolve(from, target string) (string, bool) {
	target = strings.TrimSpace(target)
	dir := path.Dir(from)
	if i := strings.Index(target, "$"); i > 0 {
		fam, rest := target[:i], target[i+1:]
		module := ""
		if j := strings.LastIndex(fam, ":"); j >= 0 {
			module, fam = fam[:j], fam[j+1:]
			if k := strings.LastIndex(module, ":"); k >= 0 {
				module = module[k+1:]
			}
		}
		folder := map[string]string{"partial": "partials", "example": "examples", "page": "pages", "image": "images", "attachment": "attachments"}[fam]
		// the module folder is the parent of the nearest "pages" (or partials …) folder
		d := dir
		for d != "." && d != "/" && d != "" {
			base := path.Base(d)
			if base == "pages" || base == "partials" || base == "examples" {
				mod := path.Dir(d)
				if module != "" {
					mod = path.Join(path.Dir(mod), module)
				}
				return path.Join(mod, folder, rest), true
			}
			d = path.Dir(d)
		}
		return path.Join(folder, rest), true
	}
	if strings.HasPrefix(target, "/") {
		return path.Clean(target[1:]), true
	}
	return path.Clean(path.Join(dir, target)), true
}

func (p *pre) include(l srcLine, target, attrs string, offset int) {
	target = p.subst(target, l)
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		p.item(l, "include", Review, fmt.Sprintf("remote include %s is not fetched; a placeholder comment is left", target))
		p.emit(srcLine{fmt.Sprintf("<!-- include: %s (remote, not fetched) -->", target), l.file, l.n})
		return
	}
	rel, _ := p.resolve(l.file, target)
	b, err := os.ReadFile(filepath.Join(p.c.root, filepath.FromSlash(rel)))
	if err != nil {
		p.item(l, "include", Review, fmt.Sprintf("include %s not found (looked for %s); a placeholder comment is left", target, rel))
		p.emit(srcLine{fmt.Sprintf("<!-- include not found: %s -->", target), l.file, l.n})
		return
	}
	if p.depth > 32 {
		p.item(l, "include", Review, "includes nested more than 32 deep; stopped")
		return
	}
	p.doc.includes = append(p.doc.includes, rel)
	opts := parseAttrList(attrs)
	inc := splitLines(string(b), rel)
	if v := opts["lines"]; v != "" {
		inc = selectLines(inc, v)
	}
	tags := opts["tags"]
	if tags == "" {
		tags = opts["tag"]
	}
	if tags != "" {
		inc = selectTags(inc, tags)
	}
	var kept []srcLine
	for _, x := range inc {
		if !tagMarker.MatchString(x.text) || !strings.Contains(x.text, "[]") {
			kept = append(kept, x)
		}
	}
	if v := opts["indent"]; v == "0" {
		kept = dedent(kept)
	}
	lo := offset
	if v := opts["leveloffset"]; v != "" {
		lo = levelOffset(v, offset)
	}
	p.depth++
	p.lines(kept, lo)
	p.depth--
}

func parseAttrList(s string) map[string]string {
	m := map[string]string{}
	for _, part := range splitAttrs(s) {
		if k, v, ok := strings.Cut(part, "="); ok {
			m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return m
}

// splitAttrs splits on commas outside quotes.
func splitAttrs(s string) []string {
	var out []string
	var cur strings.Builder
	q := rune(0)
	for _, r := range s {
		switch {
		case q != 0:
			if r == q {
				q = 0
			}
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			q = r
			cur.WriteRune(r)
		case r == ',':
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

func selectLines(in []srcLine, spec string) []srcLine {
	var out []srcLine
	for _, r := range strings.FieldsFunc(spec, func(r rune) bool { return r == ';' || r == ',' }) {
		a, b, isRange := strings.Cut(strings.TrimSpace(r), "..")
		from, _ := strconv.Atoi(a)
		to := from
		if isRange {
			to, _ = strconv.Atoi(b)
			if to == -1 || b == "" {
				to = len(in)
			}
		}
		for i := max(from, 1); i <= min(to, len(in)); i++ {
			out = append(out, in[i-1])
		}
	}
	return out
}

func selectTags(in []srcLine, spec string) []srcLine {
	want := map[string]bool{}
	for _, t := range strings.Split(spec, ";") {
		want[strings.TrimSpace(t)] = true
	}
	var out []srcLine
	depth := 0
	for _, l := range in {
		if m := tagMarker.FindStringSubmatch(l.text); m != nil && want[m[2]] {
			if m[1] == "tag" {
				depth++
			} else if depth > 0 {
				depth--
			}
			continue
		}
		if depth > 0 {
			out = append(out, l)
		}
	}
	return out
}

func dedent(in []srcLine) []srcLine {
	minIndent := -1
	for _, l := range in {
		if strings.TrimSpace(l.text) == "" {
			continue
		}
		n := len(l.text) - len(strings.TrimLeft(l.text, " \t"))
		if minIndent < 0 || n < minIndent {
			minIndent = n
		}
	}
	if minIndent <= 0 {
		return in
	}
	out := make([]srcLine, len(in))
	for i, l := range in {
		if len(l.text) >= minIndent {
			l.text = l.text[minIndent:]
		}
		out[i] = l
	}
	return out
}
