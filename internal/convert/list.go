package convert

import (
	"strings"
)

type listMark struct {
	kind   string // ul, ol, dl
	marker string
}

func markOf(t string) (listMark, string, string, bool) {
	if m := listItem.FindStringSubmatch(t); m != nil {
		k := "ul"
		if strings.HasPrefix(m[1], ".") || strings.ContainsAny(m[1][len(m[1])-1:], ".)") && m[1] != "-" && !strings.HasPrefix(m[1], "*") {
			k = "ol"
		}
		marker := m[1]
		if k == "ol" && !strings.HasPrefix(marker, ".") {
			marker = "1." // explicit numbering: one level per style
		}
		return listMark{k, marker}, "", m[2], true
	}
	if isDlist(t) {
		m := dlistItem.FindStringSubmatch(t)
		return listMark{"dl", m[2]}, m[1], m[3], true
	}
	return listMark{}, "", "", false
}

// list renders the list starting at lines[i] and returns the next index.
func (r *renderer) list(lines []srcLine, i int) (string, int) {
	var out []string
	var stack []listMark
	var cols []int // content column for each level
	defList := r.c.opt.Target == TargetMkDocs || r.c.opt.Target == TargetHugo
	// Python-Markdown (MkDocs) needs nested blocks indented four spaces
	width := func(marker int) int {
		if r.c.opt.Target == TargetMkDocs {
			return 4
		}
		return marker
	}
	reportedDL := false
	j := i
	for j < len(lines) {
		l := lines[j]
		t := l.text
		if strings.TrimSpace(t) == "" {
			k := j
			for k < len(lines) && strings.TrimSpace(lines[k].text) == "" {
				k++
			}
			if k < len(lines) {
				if _, _, _, ok := markOf(lines[k].text); ok {
					j = k
					continue
				}
			}
			break
		}
		mk, term, text, ok := markOf(t)
		if !ok {
			break
		}
		// depth: a marker seen before closes deeper levels; a new one nests
		depth := -1
		for d, s := range stack {
			if s == mk {
				depth = d
				break
			}
		}
		if depth < 0 {
			stack = append(stack, mk)
			depth = len(stack) - 1
		} else {
			stack = stack[:depth+1]
		}
		cols = cols[:min(len(cols), depth)]
		base := 0
		if depth > 0 && len(cols) >= depth {
			base = cols[depth-1]
		}
		pad := strings.Repeat(" ", base)
		// the item's own text continues on following lines
		k := j + 1
		textLines := []srcLine{{text, l.file, l.n}}
		for k < len(lines) {
			n := lines[k].text
			if strings.TrimSpace(n) == "" || n == "+" || delimRe.MatchString(n) || blockAttrs.MatchString(n) {
				break
			}
			if _, _, _, ok := markOf(n); ok {
				break
			}
			textLines = append(textLines, srcLine{strings.TrimSpace(n), lines[k].file, lines[k].n})
			k++
		}
		var head string
		var col int
		switch mk.kind {
		case "dl":
			termMD := r.inline(term, l)
			body := ""
			if strings.TrimSpace(text) != "" || len(textLines) > 1 {
				body = r.paragraph(trimFirstEmpty(textLines), &pending{})
			}
			if defList && depth == 0 {
				head = pad + termMD
				if len(out) > 0 {
					head = "\n" + head // a term straight after a definition would read as part of it
				}
				col = base + 4
				if body != "" {
					head += "\n" + pad + ":   " + indentAfterFirst(body, col)
				}
			} else {
				if !reportedDL {
					r.once(l, "description list", Approximated, "description lists become bulleted lists with the term in bold (--target mkdocs or hugo writes definition-list syntax)")
					reportedDL = true
				}
				col = base + width(2)
				head = pad + "- **" + termMD + "**"
				if body != "" {
					head += ": " + indentAfterFirst(body, col)
				}
			}
		case "ol":
			col = base + width(3)
			head = pad + "1. " + indentAfterFirst(r.paragraph(textLines, &pending{}), col)
		default:
			col = base + width(2)
			body := r.paragraph(textLines, &pending{})
			if m := checkItem.FindStringSubmatch(text); m != nil {
				box := "[ ]"
				if m[1] != " " {
					box = "[x]"
				}
				body = box + " " + r.paragraph(append([]srcLine{{m[2], l.file, l.n}}, textLines[1:]...), &pending{})
			}
			head = pad + "- " + indentAfterFirst(body, col)
		}
		cols = append(cols, col)
		out = append(out, head)
		// attached blocks: "+" then one block; an indented (literal) paragraph
		// after the item attaches too, even across blank lines
		for k < len(lines) {
			if lines[k].text != "+" {
				b := k
				for b < len(lines) && strings.TrimSpace(lines[b].text) == "" {
					b++
				}
				if b == len(lines) || !(strings.HasPrefix(lines[b].text, " ") || strings.HasPrefix(lines[b].text, "\t")) {
					break
				}
				if _, _, _, ok := markOf(strings.TrimSpace(lines[b].text)); ok {
					break
				}
				end := b
				var code []string
				for end < len(lines) && strings.TrimSpace(lines[end].text) != "" && lines[end].text != "+" {
					code = append(code, lines[end].text)
					end++
				}
				out = append(out, "", indent(fence("", dedentStrings(code)), col))
				k = end
				continue
			}
			start := k + 1
			end := start
			// block attribute and title lines belong to the block
			for end < len(lines) && (blockAttrs.MatchString(lines[end].text) || blockTitle.MatchString(lines[end].text) && !strings.HasPrefix(lines[end].text, "..")) {
				end++
			}
			if end < len(lines) && delimRe.MatchString(lines[end].text) {
				d := lines[end].text
				end++
				for end < len(lines) && lines[end].text != d {
					end++
				}
				end++
			} else {
				for end < len(lines) && strings.TrimSpace(lines[end].text) != "" && lines[end].text != "+" {
					if _, _, _, ok := markOf(lines[end].text); ok && end > start {
						break
					}
					end++
				}
			}
			end = min(end, len(lines))
			block := r.blocks(lines[start:end])
			out = append(out, "", indent(block, col))
			k = end
		}
		j = k
	}
	return strings.Join(out, "\n"), j
}

func trimFirstEmpty(ls []srcLine) []srcLine {
	if len(ls) > 0 && strings.TrimSpace(ls[0].text) == "" {
		return ls[1:]
	}
	return ls
}

func indentAfterFirst(s string, n int) string {
	lines := strings.Split(s, "\n")
	pad := strings.Repeat(" ", n)
	for i := 1; i < len(lines); i++ {
		if lines[i] != "" {
			lines[i] = pad + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}
