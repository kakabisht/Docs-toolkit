package convert

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	passMacro   = regexp.MustCompile(`pass:[a-z,]*\[(.*?)\]`)
	triplePlus  = regexp.MustCompile(`\+\+\+(.+?)\+\+\+`)
	doublePlus  = regexp.MustCompile(`\+\+(.+?)\+\+`)
	singlePlus  = regexp.MustCompile("(^|[^\\w+`])\\+([^\\s+](?:[^+]*?[^\\s+])?)\\+($|[^\\w+])")
	codePlus    = regexp.MustCompile("``?\\+(.+?)\\+``?")
	codeSpan    = regexp.MustCompile("``(.+?)``|(^|[^\\w`])`([^`\\s](?:[^`]*?[^`\\s])?)`($|[^\\w`])")
	kbdMacro    = regexp.MustCompile(`kbd:\[(.*?)\]`)
	btnMacro    = regexp.MustCompile(`btn:\[(.*?)\]`)
	menuMacro   = regexp.MustCompile(`menu:([^\[\s]+)\[(.*?)\]`)
	footMacro   = regexp.MustCompile(`footnote(?:ref)?:([\w-]*)\[((?:[^\]\\]|\\.)*)\]`)
	imgInline   = regexp.MustCompile(`image:([^:\s\[][^\s\[]*)\[(.*?)\]`)
	xrefMacro   = regexp.MustCompile(`xref:([^\s\[]+)\[(.*?)\]`)
	linkMacro   = regexp.MustCompile(`(^|[^\w:/])link:([^\s\[]+)\[(.*?)\]`)
	mailMacro   = regexp.MustCompile(`mailto:([^\s\[]+)\[(.*?)\]`)
	urlText     = regexp.MustCompile(`(^|[^\w(<"'=/])((?:https?|ftp|irc)://[^\s\[\]<>]+)\[(.*?)\]`)
	bareURL     = regexp.MustCompile(`(^|[\s(])((?:https?|ftp)://[^\s<>\[\]]*[^\s<>\[\].,;:!?)])`)
	xrefShort   = regexp.MustCompile(`<<([^,>]+?)(?:,\s*(.+?))?>>`)
	indexTerm3  = regexp.MustCompile(`\(\(\((.+?)\)\)\)`)
	indexTerm2  = regexp.MustCompile(`\(\((.+?)\)\)`)
	indexMacro  = regexp.MustCompile(`indexterm2?:\[(.*?)\]`)
	stemMacro   = regexp.MustCompile(`(?:stem|latexmath|asciimath):\[(.*?)\]`)
	iconMacro   = regexp.MustCompile(`icon:([\w-]+)\[(.*?)\]`)
	roleSpan    = regexp.MustCompile(`\[\.([\w.-]+)\]#(.+?)#`)
	highlight   = regexp.MustCompile(`(^|[^\w#])#([^#\s](?:[^#]*?[^#\s])?)#($|[^\w#])`)
	boldU       = regexp.MustCompile(`\*\*(.+?)\*\*`)
	boldC       = regexp.MustCompile(`(^|[^\w*\\])\*([^*\s](?:[^*]*?[^*\s])?)\*($|[^\w*])`)
	italicU     = regexp.MustCompile(`__(.+?)__`)
	supRe       = regexp.MustCompile(`\^([^\s^]+)\^`)
	subRe       = regexp.MustCompile(`(^|[^~])~([^\s~/]+)~($|[^~])`)
	smartDouble = regexp.MustCompile("\"`(.+?)`\"")
	smartSingle = regexp.MustCompile("'`(.+?)`'")
	emDash      = regexp.MustCompile(`(\w) -- (\w)|(\w)--(\w)`)
	htmlish     = regexp.MustCompile(`<([A-Za-z/!])`)
)

type protector struct {
	vals []string
}

func (p *protector) put(s string) string {
	p.vals = append(p.vals, s)
	return fmt.Sprintf("\x00%d\x00", len(p.vals)-1)
}

var tokenRe = regexp.MustCompile("\x00(\\d+)\x00")

func (p *protector) restore(s string) string {
	for i := 0; i < 4 && strings.Contains(s, "\x00"); i++ {
		s = tokenRe.ReplaceAllStringFunc(s, func(m string) string {
			var n int
			fmt.Sscanf(m[1:len(m)-1], "%d", &n)
			if n >= len(p.vals) {
				return m
			}
			return p.vals[n]
		})
	}
	return s
}

func codeOf(s string) string {
	f := "`"
	for strings.Contains(s, f) {
		f += "`"
	}
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return f + " " + s + " " + f
	}
	return f + s + f
}

// stripInline turns inline markup into plain text (for front matter).
func stripInline(s string) string {
	s = strings.NewReplacer("`", "", "**", "", "__", "").Replace(s)
	return strings.TrimSpace(plainText(s))
}

// inline converts AsciiDoc inline markup to Markdown.
func (r *renderer) inline(s string, l srcLine) string {
	if s == "" {
		return ""
	}
	p := &r.prot
	// passthroughs and code first: nothing inside them is markup
	s = passMacro.ReplaceAllStringFunc(s, func(m string) string {
		r.once(l, "passthrough", Review, "inline passthrough kept as raw text or HTML")
		return p.put(passMacro.FindStringSubmatch(m)[1])
	})
	// smart quotes before code spans: "`x`" would otherwise read as code
	s = smartDouble.ReplaceAllString(s, "“$1”")
	s = smartSingle.ReplaceAllString(s, "‘$1’")
	s = triplePlus.ReplaceAllStringFunc(s, func(m string) string { return p.put(triplePlus.FindStringSubmatch(m)[1]) })
	s = codePlus.ReplaceAllStringFunc(s, func(m string) string { return p.put(codeOf(codePlus.FindStringSubmatch(m)[1])) })
	s = codeSpan.ReplaceAllStringFunc(s, func(m string) string {
		sm := codeSpan.FindStringSubmatch(m)
		if sm[1] != "" {
			return p.put(codeOf(sm[1]))
		}
		return sm[2] + p.put(codeOf(sm[3])) + sm[4]
	})
	s = doublePlus.ReplaceAllStringFunc(s, func(m string) string { return p.put(mdEscape(doublePlus.FindStringSubmatch(m)[1])) })
	s = singlePlus.ReplaceAllStringFunc(s, func(m string) string {
		sm := singlePlus.FindStringSubmatch(m)
		return sm[1] + p.put(mdEscape(sm[2])) + sm[3]
	})
	// macros
	s = kbdMacro.ReplaceAllStringFunc(s, func(m string) string {
		keys := strings.FieldsFunc(kbdMacro.FindStringSubmatch(m)[1], func(r rune) bool { return r == '+' || r == ',' })
		for i, k := range keys {
			keys[i] = "<kbd>" + strings.TrimSpace(k) + "</kbd>"
		}
		return p.put(strings.Join(keys, "+"))
	})
	s = btnMacro.ReplaceAllStringFunc(s, func(m string) string {
		return p.put("**" + btnMacro.FindStringSubmatch(m)[1] + "**")
	})
	s = menuMacro.ReplaceAllStringFunc(s, func(m string) string {
		sm := menuMacro.FindStringSubmatch(m)
		items := []string{sm[1]}
		for _, x := range strings.Split(sm[2], ">") {
			if x = strings.TrimSpace(x); x != "" {
				items = append(items, x)
			}
		}
		r.once(l, "menu", Approximated, "menu selections become bold text joined by >")
		return p.put("**" + strings.Join(items, " > ") + "**")
	})
	s = footMacro.ReplaceAllStringFunc(s, func(m string) string {
		sm := footMacro.FindStringSubmatch(m)
		id, text := sm[1], strings.ReplaceAll(sm[2], `\]`, "]")
		if strings.HasPrefix(m, "footnoteref:") {
			id, text, _ = strings.Cut(sm[2], ",")
		}
		if n, ok := r.fnIDs[id]; ok && id != "" {
			return p.put(fmt.Sprintf("[^%d]", n))
		}
		r.footnotes = append(r.footnotes, r.inline(text, l))
		n := len(r.footnotes)
		if id != "" {
			r.fnIDs[id] = n
		}
		return p.put(fmt.Sprintf("[^%d]", n))
	})
	s = imgInline.ReplaceAllStringFunc(s, func(m string) string {
		sm := imgInline.FindStringSubmatch(m)
		alt := strings.Trim(strings.TrimSpace(splitAttrs(sm[2])[0]), `"`)
		if alt == "" {
			alt = strings.TrimSuffix(path.Base(sm[1]), path.Ext(sm[1]))
		}
		return p.put(fmt.Sprintf("![%s](%s)", escapeBrackets(alt), mdURL(r.imagePath(sm[1], l))))
	})
	s = xrefMacro.ReplaceAllStringFunc(s, func(m string) string {
		sm := xrefMacro.FindStringSubmatch(m)
		return p.put(r.xref(sm[1], sm[2], l))
	})
	s = xrefShort.ReplaceAllStringFunc(s, func(m string) string {
		sm := xrefShort.FindStringSubmatch(m)
		return p.put(r.xref(sm[1], sm[2], l))
	})
	s = mailMacro.ReplaceAllStringFunc(s, func(m string) string {
		sm := mailMacro.FindStringSubmatch(m)
		text := firstNonEmptyStr(strings.Split(sm[2], ",")[0], sm[1])
		return p.put(fmt.Sprintf("[%s](mailto:%s)", r.inline(text, l), sm[1]))
	})
	s = linkMacro.ReplaceAllStringFunc(s, func(m string) string {
		sm := linkMacro.FindStringSubmatch(m)
		target, text := sm[2], sm[3]
		// some sources swap the two: link:Display[https://url]
		if !strings.Contains(target, "://") && strings.Contains(text, "://") && !strings.Contains(text, " ") {
			target, text = text, target
			r.item(l, "link", Approximated, "link:text[url] with swapped arguments read as link:url[text]")
		}
		if strings.HasSuffix(strings.ToLower(target), ".adoc") || strings.Contains(strings.ToLower(target), ".adoc#") {
			return sm[1] + p.put(r.xref(target, text, l))
		}
		return sm[1] + p.put(r.link(target, text, l))
	})
	s = urlText.ReplaceAllStringFunc(s, func(m string) string {
		sm := urlText.FindStringSubmatch(m)
		return sm[1] + p.put(r.link(sm[2], sm[3], l))
	})
	s = bareURL.ReplaceAllStringFunc(s, func(m string) string {
		sm := bareURL.FindStringSubmatch(m)
		return sm[1] + p.put(sm[2])
	})
	s = inlineAnchor.ReplaceAllStringFunc(s, func(m string) string {
		sm := inlineAnchor.FindStringSubmatch(m)
		return p.put(fmt.Sprintf(`<a id="%s"></a>`, sm[1]+sm[2]))
	})
	if indexTerm3.MatchString(s) || indexMacro.MatchString(s) {
		r.once(l, "index term", Dropped, "index terms are not carried over")
		s = indexTerm3.ReplaceAllString(s, "")
		s = indexMacro.ReplaceAllString(s, "")
	}
	if indexTerm2.MatchString(s) {
		r.once(l, "index term", Dropped, "index entries are dropped; the visible term is kept")
		s = indexTerm2.ReplaceAllString(s, "$1")
	}
	s = stemMacro.ReplaceAllStringFunc(s, func(m string) string {
		r.once(l, "math", Approximated, "inline math becomes $…$; the site needs a maths extension")
		return p.put("$" + stemMacro.FindStringSubmatch(m)[1] + "$")
	})
	s = iconMacro.ReplaceAllStringFunc(s, func(m string) string {
		sm := iconMacro.FindStringSubmatch(m)
		r.once(l, "icon", Review, "icon macros have no Markdown equivalent; the icon name is kept in brackets")
		return "[" + sm[1] + "]"
	})
	// formatting
	s = roleSpan.ReplaceAllStringFunc(s, func(m string) string {
		sm := roleSpan.FindStringSubmatch(m)
		r.once(l, "role", Dropped, fmt.Sprintf("text role .%s has no Markdown equivalent; the text is kept", sm[1]))
		return sm[2]
	})
	s = highlight.ReplaceAllStringFunc(s, func(m string) string {
		sm := highlight.FindStringSubmatch(m)
		r.once(l, "highlight", Approximated, "highlighted text becomes <mark>")
		return sm[1] + p.put("<mark>") + sm[2] + p.put("</mark>") + sm[3]
	})
	s = boldU.ReplaceAllStringFunc(s, func(m string) string { return p.put("**") + boldU.FindStringSubmatch(m)[1] + p.put("**") })
	for i := 0; i < 2; i++ { // twice: adjacent matches share a boundary character
		s = boldC.ReplaceAllStringFunc(s, func(m string) string {
			sm := boldC.FindStringSubmatch(m)
			return sm[1] + p.put("**") + sm[2] + p.put("**") + sm[3]
		})
	}
	s = italicU.ReplaceAllString(s, "_${1}_")
	if supRe.MatchString(s) || subRe.MatchString(s) {
		r.once(l, "superscript", Approximated, "superscript and subscript become <sup> and <sub>")
		s = supRe.ReplaceAllStringFunc(s, func(m string) string {
			return p.put("<sup>") + supRe.FindStringSubmatch(m)[1] + p.put("</sup>")
		})
		s = subRe.ReplaceAllStringFunc(s, func(m string) string {
			sm := subRe.FindStringSubmatch(m)
			return sm[1] + p.put("<sub>") + sm[2] + p.put("</sub>") + sm[3]
		})
	}
	s = strings.NewReplacer("(C)", "©", "(R)", "®", "(TM)", "™", "...", "…").Replace(s)
	s = emDash.ReplaceAllString(s, "$1$3 — $2$4")
	s = strings.ReplaceAll(s, " -- ", " — ")
	// prose "<word" would read as HTML in Markdown
	s = htmlish.ReplaceAllString(s, "&lt;$1")
	return p.restore(s)
}

var mdSpecial = strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "[", `\[`, "]", `\]`, "<", "&lt;", "#", `\#`)

func mdEscape(s string) string { return mdSpecial.Replace(s) }

func (r *renderer) link(target, text string, l srcLine) string {
	attrs := splitAttrs(text)
	label := strings.Trim(strings.TrimSpace(attrs[0]), `"`)
	if strings.HasSuffix(label, "^") {
		label = strings.TrimSuffix(label, "^")
		r.once(l, "link window", Dropped, "links that open a new window (^ or window=) open in the same window")
	} else if strings.Contains(text, "window=") {
		r.once(l, "link window", Dropped, "links that open a new window (^ or window=) open in the same window")
	}
	if strings.Contains(label, "=") && len(attrs) == 1 && !strings.Contains(label, " ") {
		label = ""
	}
	if label == "" {
		return "<" + target + ">"
	}
	return fmt.Sprintf("[%s](%s)", r.inline(label, l), mdURL(target))
}

// xref resolves a cross reference: file.adoc#id, Antora module:page.adoc,
// or an id in this document.
func (r *renderer) xref(target, text string, l srcLine) string {
	target = strings.TrimSpace(target)
	text = strings.TrimSpace(text)
	file, frag, hasFrag := strings.Cut(target, "#")
	if !hasFrag && !strings.Contains(file, ".adoc") && !strings.Contains(file, "$") && !strings.Contains(file, ":") {
		frag, file = file, ""
	}
	src := r.d.file
	href := ""
	if file != "" {
		resolved, note := r.resolvePage(file)
		if note != "" {
			r.item(l, "cross reference", Review, note)
		}
		src = resolved
		rel := relPath(path.Dir(r.d.file), r.c.outPath(resolved))
		href = rel
	}
	if frag != "" {
		ids := r.c.ids[src]
		if a, ok := ids[frag]; ok {
			href += "#" + a
		} else if id, ok := r.c.idByTitle(src, frag); ok {
			// <<Section Title>> refers to a section by its title
			href += "#" + ids[id]
			if text == "" {
				text = frag
			}
		} else {
			if ids != nil || file == "" {
				r.item(l, "cross reference", Review, fmt.Sprintf("anchor %q not found in %s; kept as written", frag, firstNonEmptyStr(src, "this file")))
			}
			href += "#" + frag
		}
	}
	if text == "" {
		if t, ok := r.c.titles[src][frag]; ok {
			text = t
		} else if frag == "" && r.c.docT[src] != "" {
			text = r.c.docT[src]
		} else {
			text = firstNonEmptyStr(frag, strings.TrimSuffix(path.Base(file), ".adoc"))
			r.once(l, "cross reference text", Review, "a cross reference without text uses the target's id or file name")
		}
	}
	return fmt.Sprintf("[%s](%s)", r.inline(text, l), mdURL(href))
}

// resolvePage maps an xref file to a source path relative to the root.
func (r *renderer) resolvePage(file string) (string, string) {
	file = strings.TrimPrefix(file, "./")
	// Antora: version@component:module:page$file.adoc
	if strings.Contains(file, ":") || strings.Contains(file, "$") || strings.Contains(file, "@") {
		if i := strings.Index(file, "@"); i >= 0 {
			file = file[i+1:]
		}
		parts := strings.Split(strings.ReplaceAll(file, "page$", ""), ":")
		page := parts[len(parts)-1]
		mod := antoraModule(r.d.file)
		if mod == "" {
			return path.Clean(path.Join(path.Dir(r.d.file), page)), "Antora resource id used outside an Antora pages folder: " + file
		}
		modules := path.Dir(mod)
		module := path.Base(mod)
		note := ""
		switch len(parts) {
		case 2:
			module = parts[0]
		case 3:
			module = parts[1]
			note = fmt.Sprintf("cross reference to another component (%s): linked as if it were in this one", parts[0])
		}
		if module == "ROOT" || module == "" {
			module = "ROOT"
		}
		return path.Join(modules, module, "pages", page), note
	}
	if mod := antoraModule(r.d.file); mod != "" && !strings.HasPrefix(file, "../") && !strings.HasPrefix(file, "/") {
		// Antora page references are relative to the module's pages folder
		return path.Join(mod, "pages", file), ""
	}
	return path.Clean(path.Join(path.Dir(r.d.file), file)), ""
}

func relPath(fromDir, to string) string {
	if fromDir == "." {
		return to
	}
	fromParts := strings.Split(fromDir, "/")
	toParts := strings.Split(to, "/")
	i := 0
	for i < len(fromParts) && i < len(toParts)-1 && fromParts[i] == toParts[i] {
		i++
	}
	return strings.Repeat("../", len(fromParts)-i) + strings.Join(toParts[i:], "/")
}
