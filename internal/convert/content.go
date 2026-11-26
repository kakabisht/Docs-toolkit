package convert

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// The content check is a safety net that runs after every conversion: it
// looks for words in the AsciiDoc that are nowhere in the Markdown. It does
// not judge formatting, only whether text went missing without a report.

var (
	ccSkipLine  = regexp.MustCompile(`^(//|:[\w-]+!?:|\[.*\]\s*$|\[\[.*\]\]\s*$|(?:include|ifdef|ifndef|ifeval|endif|toc)::|[=\-.*_+/|,:]{4,}\s*$|\|===|[,:!]===)`)
	ccMacro     = regexp.MustCompile(`\b(?:xref|link|image|footnote|footnoteref|kbd|btn|menu|mailto|anchor|indexterm2?|pass|stem|latexmath|asciimath|icon|video|audio|https?|ftp|irc):[^\s\[]*`)
	ccAttrRef   = regexp.MustCompile(`\{[\w-]+\}`)
	ccXrefShort = regexp.MustCompile(`<<[^,>]*,?`)
	ccAnchor    = regexp.MustCompile(`\[\[[^\]]*\]\]|\[#[^\]]*\]`)
	ccIndex3    = regexp.MustCompile(`\(\(\([^)]*\)\)\)`)
	ccSpec      = regexp.MustCompile(`^\s*(?:\d+\*)?(?:\d*\.?\d+\+)?[<^>]?(?:\.[<^>])?[adehlmsv]?\|`)
	ccAttrList  = regexp.MustCompile(`\[[^\]]*=[^\]]*\]`)
)

// words splits s into lower-case words of three or more letters or digits.
func words(s string) []string {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := f[:0]
	for _, w := range f {
		if len([]rune(w)) >= 3 {
			out = append(out, w)
		}
	}
	return out
}

// contentCheck reports source lines whose words are missing from md. Lines
// already reported as dropped are skipped: their loss is on record.
func (c *Converter) contentCheck(d *document, md string) {
	have := map[string]bool{}
	for _, w := range words(md) {
		have[w] = true
	}
	dropped := map[string]bool{}
	for _, it := range c.report.Items {
		if it.Action == Dropped {
			dropped[fmt.Sprintf("%s:%d", it.File, it.Line)] = true
		}
	}
	for _, l := range d.lines {
		t := strings.TrimSpace(l.text)
		if t == "" || ccSkipLine.MatchString(t) || dropped[fmt.Sprintf("%s:%d", l.file, l.n)] {
			continue
		}
		t = ccIndex3.ReplaceAllString(t, " ")
		t = ccMacro.ReplaceAllString(t, " ")
		t = ccAttrRef.ReplaceAllString(t, " ")
		t = ccXrefShort.ReplaceAllString(t, " ")
		t = ccAnchor.ReplaceAllString(t, " ")
		t = ccAttrList.ReplaceAllString(t, " ")
		t = ccSpec.ReplaceAllString(t, " ")
		var missing []string
		seen := map[string]bool{}
		for _, w := range words(t) {
			if !have[w] && !seen[w] {
				missing = append(missing, w)
				seen[w] = true
			}
		}
		if len(missing) > 0 {
			c.report.add(Item{File: l.file, Line: l.n, Construct: "content check", Action: Review,
				Message: "words in the source not found in the Markdown: " + strings.Join(missing, ", ")})
		}
	}
}
