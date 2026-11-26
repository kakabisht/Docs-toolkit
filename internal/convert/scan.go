package convert

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/kakabisht/Docs-toolkit/internal/parse"
)

var (
	sectionRe     = regexp.MustCompile(`^(={1,6}|#{1,6})\s+(\S.*?)\s*(?:=+|#+)?$`)
	blockAnchorRe = regexp.MustCompile(`^\[\[([A-Za-z_:][\w:.-]*)(?:,\s*(.*?))?\]\]\s*$`)
	idAttrRe      = regexp.MustCompile(`^\[(?:[^\]#,]*?)#([A-Za-z_][\w:.-]*)`)
	idNamedRe     = regexp.MustCompile(`\bid=["']?([A-Za-z_][\w:.-]*)`)
	inlineAnchor  = regexp.MustCompile(`\[\[([A-Za-z_:][\w:.-]*)(?:,[^\]]*)?\]\]|anchor:([A-Za-z_:][\w:.-]*)\[[^\]]*\]`)
	markupStrip   = regexp.MustCompile("[`*_#^~+]|<[^>]+>|\\[[^\\]]*\\]")
)

// autoID is Asciidoctor's generated section id.
func autoID(title, prefix, sep string) string {
	t := strings.ToLower(markupStrip.ReplaceAllString(title, ""))
	var b strings.Builder
	b.WriteString(prefix)
	last := true
	for _, r := range t {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' && sep != "_" {
			b.WriteRune(r)
			last = false
		} else if !last && sep != "" {
			b.WriteString(sep)
			last = true
		}
	}
	return strings.TrimSuffix(b.String(), sep)
}

// scan records every section id of a document and the Markdown anchor the
// heading will get on the target site.
func (c *Converter) scan(d *document) {
	ids := map[string]string{}
	titles := map[string]string{}
	usedMD := map[string]int{}
	usedAD := map[string]int{}
	prefix, sep := d.attrs["idprefix"], d.attrs["idseparator"]
	verbatim := ""
	pending, blockT := "", ""
	// a block id (on a table, listing, example …) is written as <a id> before the block
	blockID := func() {
		if pending != "" {
			ids[pending] = pending
			if blockT != "" {
				titles[pending] = plainText(blockT)
			}
		}
		pending, blockT = "", ""
	}
	for _, l := range d.lines {
		t := l.text
		if verbatim != "" {
			if t == verbatim {
				verbatim = ""
			}
			continue
		}
		if m := blockTitle.FindStringSubmatch(t); m != nil && !strings.HasPrefix(t, "..") {
			blockT = m[1]
			continue
		}
		if verbatimDel.MatchString(t) {
			blockID()
			verbatim = t
			continue
		}
		if m := blockAnchorRe.FindStringSubmatch(t); m != nil {
			pending = m[1]
			continue
		}
		if blockAttrs.MatchString(t) {
			if m := idAttrRe.FindStringSubmatch(t); m != nil {
				pending = m[1]
			} else if m := idNamedRe.FindStringSubmatch(t); m != nil {
				pending = m[1]
			}
			continue
		}
		if m := sectionRe.FindStringSubmatch(t); m != nil {
			title := m[2]
			md := parse.SlugifyStyle(plainText(title), c.opt.AnchorStyle)
			if n := usedMD[md]; n > 0 {
				suffix := fmt.Sprintf("-%d", n)
				if c.opt.AnchorStyle == parse.StyleMkDocs {
					suffix = fmt.Sprintf("_%d", n)
				}
				usedMD[md]++
				md += suffix
			} else {
				usedMD[md] = 1
			}
			id := pending
			if id == "" {
				id = autoID(title, prefix, sep)
				if n := usedAD[id]; n > 0 {
					usedAD[id]++
					id = fmt.Sprintf("%s_%d", id, n+1)
				} else {
					usedAD[id] = 1
				}
				ids[id] = md
			} else {
				ids[id] = id // explicit ids are kept on the heading
			}
			titles[id] = plainText(title)
			pending, blockT = "", ""
			continue
		}
		for _, m := range inlineAnchor.FindAllStringSubmatch(t, -1) {
			id := m[1] + m[2]
			ids[id] = id
		}
		if t != "" {
			blockID()
		}
	}
	c.ids[d.file] = ids
	c.titles[d.file] = titles
	c.docT[d.file] = d.title
}

var plainRe = regexp.MustCompile("`([^`]*)`|\\*\\*?([^*]+)\\*\\*?|__?([^_]+)__?")

// plainText removes inline markup so heading slugs match what the site makes.
func plainText(s string) string {
	s = plainRe.ReplaceAllString(s, "$1$2$3")
	return strings.TrimSpace(s)
}
