package convert

import (
	"encoding/csv"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type cell struct {
	text    string
	colspan int
	rowspan int
	style   string // a (AsciiDoc), l (literal), m (monospace), e, s, h …
	align   string
	line    srcLine
}

var cellSpec = regexp.MustCompile(`(?:^|\s)((?:(\d+)\*)?(?:(\d+)(?:\.(\d+))?\+|\.(\d+)\+)?([<^>])?(?:\.([<^>]))?([adehlmsv])?)$`)

// table renders a table as a GFM pipe table when it is simple, and as HTML
// when it has merged cells or block content.
func (r *renderer) table(delim string, inner []srcLine, p *pending, l srcLine) string {
	sep := string(delim[0])
	switch strings.Trim(p.named("format"), `"'`) {
	case "csv":
		sep = ","
	case "tsv":
		sep = "\t"
	case "dsv":
		sep = ":"
	}
	if s := strings.Trim(p.named("separator"), `"'`); s != "" && sep != "," {
		sep = s
	}
	cols := parseCols(p.named("cols"))
	var cells []cell
	firstLine, firstLineCells, blankAfterFirst := -1, 0, false
	switch sep {
	case ",", ":", "\t":
		var text []string
		for _, x := range inner {
			text = append(text, x.text)
		}
		rd := csv.NewReader(strings.NewReader(strings.Join(text, "\n")))
		rd.Comma = rune(sep[0])
		rd.LazyQuotes = sep != ","
		rd.FieldsPerRecord = -1
		recs, err := rd.ReadAll()
		if err != nil {
			r.item(l, "table", Review, "could not read the CSV table: "+err.Error())
		}
		for i, rec := range recs {
			if i == 0 {
				firstLineCells = len(rec)
			}
			for _, f := range rec {
				cells = append(cells, cell{text: strings.TrimSpace(f), colspan: 1, rowspan: 1, line: l})
			}
		}
		firstLine = 0
	default:
		// split on unescaped separators; a spec right before a separator
		// belongs to the next cell
		var cur strings.Builder
		var curLine srcLine
		var curSpec, nextSpec []string // spec of the open cell; spec seen for the cell after it
		started := false
		flush := func() {
			if started {
				c := cell{text: strings.TrimSpace(cur.String()), colspan: 1, rowspan: 1, line: curLine}
				applySpec(&c, curSpec)
				cells = append(cells, c)
			}
			cur.Reset()
			curSpec, nextSpec = nextSpec, nil
			started = true
		}
		for li, x := range inner {
			t := x.text
			if strings.TrimSpace(t) == "" {
				if li == 1 && firstLine == 0 {
					blankAfterFirst = true
				}
				if started {
					cur.WriteString("\n\n")
				}
				continue
			}
			if firstLine < 0 {
				firstLine = li
				firstLineCells = countSeps(t, sep)
			}
			parts := splitUnescaped(t, sep)
			if started && len(parts) == 1 {
				cur.WriteString("\n")
			}
			for k, part := range parts {
				if k == 0 {
					if len(parts) > 1 {
						if m := cellSpec.FindStringSubmatch(part); m != nil && strings.TrimSpace(part) == m[1] {
							if m[1] != "" {
								nextSpec = m
							}
							continue
						}
					}
					if started && len(parts) > 1 {
						cur.WriteString("\n")
					}
					cur.WriteString(part)
					continue
				}
				// part k starts a new cell; a trailing spec belongs to the cell after it
				body := part
				var trailing []string
				if k < len(parts)-1 {
					if m := cellSpec.FindStringSubmatch(part); m != nil && m[1] != "" {
						trailing = m
						body = strings.TrimSuffix(part, m[1])
					}
				}
				flush()
				curLine = x
				cur.WriteString(body)
				nextSpec = trailing
			}
		}
		if started {
			nextSpec = nil
			flush()
		}
	}
	ncols := len(cols)
	if ncols == 0 {
		ncols = firstLineCells
	}
	if ncols == 0 {
		return ""
	}
	// rows
	var rows [][]cell
	var row []cell
	busy := make([]int, ncols) // rows still covered by a rowspan from above
	width := 0
	startRow := func() {
		width = 0
		for i := range busy {
			if busy[i] > 0 {
				width++
			}
		}
	}
	startRow()
	for _, c := range cells {
		row = append(row, c)
		// place the cell in the first free columns
		col := 0
		for placed := 0; col < ncols && placed < 1; col++ {
			if busy[col] == 0 {
				placed++
			}
		}
		for k := col - 1; k < col-1+c.colspan && k < ncols; k++ {
			if k >= 0 && c.rowspan > 1 {
				busy[k] = c.rowspan
			}
		}
		width += c.colspan
		if width >= ncols {
			rows = append(rows, row)
			row = nil
			for i := range busy {
				if busy[i] > 0 {
					busy[i]--
				}
			}
			startRow()
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
		r.item(l, "table", Review, "the last row has fewer cells than the table has columns")
	}
	header := p.option("header") || (blankAfterFirst && firstLineCells == ncols && !p.option("noheader"))
	if p.option("noheader") {
		header = false
	}
	if p.option("footer") {
		r.once(l, "table footer", Approximated, "table footer rows become ordinary rows")
	}
	complex := false
	for _, rw := range rows {
		for _, c := range rw {
			if c.colspan > 1 || c.rowspan > 1 || c.style == "a" || c.style == "l" || strings.Contains(c.text, "\n\n") {
				complex = true
			}
		}
	}
	title := ""
	if p.title != "" {
		title = "**" + r.inline(p.title, p.line) + "**\n\n"
	}
	if complex {
		r.item(l, "table", Review, "table has merged cells or block content: written as HTML; check it renders on the target site")
		return title + r.htmlTable(rows, header)
	}
	var b strings.Builder
	cellMD := func(c cell) string {
		s := r.inline(strings.Join(strings.Fields(strings.ReplaceAll(c.text, "\n", " ")), " "), c.line)
		switch c.style {
		case "m":
			s = codeOf(c.text)
		case "s":
			s = "**" + s + "**"
		case "e":
			s = "_" + s + "_"
		}
		return strings.ReplaceAll(s, "|", `\|`)
	}
	writeRow := func(rw []cell) {
		var parts []string
		for _, c := range rw {
			parts = append(parts, cellMD(c))
		}
		for len(parts) < ncols {
			parts = append(parts, "")
		}
		b.WriteString("| " + strings.Join(parts, " | ") + " |\n")
	}
	if header && len(rows) > 0 {
		writeRow(rows[0])
		rows = rows[1:]
	} else {
		b.WriteString("|" + strings.Repeat("  |", ncols) + "\n")
		r.once(l, "table header", Approximated, "tables without a header row get an empty one (Markdown tables need a header)")
	}
	var aligns []string
	for i := 0; i < ncols; i++ {
		a := "---"
		if i < len(cols) {
			switch cols[i] {
			case "^":
				a = ":---:"
			case ">":
				a = "---:"
			}
		}
		aligns = append(aligns, a)
	}
	b.WriteString("| " + strings.Join(aligns, " | ") + " |\n")
	for _, rw := range rows {
		writeRow(rw)
	}
	return title + strings.TrimSuffix(b.String(), "\n")
}

func countSeps(t, sep string) int { return len(splitUnescaped(t, sep)) - 1 }

func splitUnescaped(t, sep string) []string {
	var parts []string
	var cur strings.Builder
	for i := 0; i < len(t); i++ {
		if t[i] == '\\' && i+1 < len(t) && string(t[i+1]) == sep {
			cur.WriteString(sep)
			i++
			continue
		}
		if string(t[i]) == sep {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(t[i])
	}
	return append(parts, cur.String())
}

func applySpec(c *cell, m []string) {
	if m == nil {
		return
	}
	if m[3] != "" {
		c.colspan, _ = strconv.Atoi(m[3])
	}
	if m[4] != "" {
		c.rowspan, _ = strconv.Atoi(m[4])
	}
	if m[5] != "" {
		c.rowspan, _ = strconv.Atoi(m[5])
	}
	c.align = m[6]
	c.style = m[8]
	if m[2] != "" {
		// n* duplicates a cell: rare, treat as one
		c.colspan = max(c.colspan, 1)
	}
}

// parseCols reads cols="1,2a,^3" into one alignment marker per column.
func parseCols(spec string) []string {
	spec = strings.Trim(spec, `"' `)
	if spec == "" {
		return nil
	}
	var out []string
	if n, err := strconv.Atoi(spec); err == nil {
		return make([]string, n)
	}
	for _, c := range strings.Split(strings.ReplaceAll(spec, ";", ","), ",") {
		c = strings.TrimSpace(c)
		mult := 1
		if a, b, ok := strings.Cut(c, "*"); ok {
			mult, _ = strconv.Atoi(a)
			c = b
		}
		al := ""
		for _, ch := range []string{"^", ">", "<"} {
			if strings.HasPrefix(c, ch) {
				al = ch
			}
		}
		for i := 0; i < max(mult, 1); i++ {
			out = append(out, al)
		}
	}
	return out
}

func (r *renderer) htmlTable(rows [][]cell, header bool) string {
	var b strings.Builder
	b.WriteString("<table>\n")
	for i, rw := range rows {
		b.WriteString("<tr>\n")
		tag := "td"
		if header && i == 0 {
			tag = "th"
		}
		for _, c := range rw {
			attrs := ""
			if c.colspan > 1 {
				attrs += fmt.Sprintf(` colspan="%d"`, c.colspan)
			}
			if c.rowspan > 1 {
				attrs += fmt.Sprintf(` rowspan="%d"`, c.rowspan)
			}
			var content string
			switch c.style {
			case "a":
				var ls []srcLine
				for k, t := range strings.Split(c.text, "\n") {
					ls = append(ls, srcLine{t, c.line.file, c.line.n + k})
				}
				content = "\n\n" + r.blocks(ls) + "\n\n"
			case "l":
				content = "<pre>" + strings.NewReplacer("&", "&amp;", "<", "&lt;").Replace(c.text) + "</pre>"
			default:
				var paras []string
				for _, para := range strings.Split(c.text, "\n\n") {
					paras = append(paras, r.inline(strings.Join(strings.Fields(para), " "), c.line))
				}
				content = strings.Join(paras, "<br><br>")
			}
			fmt.Fprintf(&b, "<%s%s>%s</%s>\n", tag, attrs, content, tag)
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("</table>")
	return b.String()
}
