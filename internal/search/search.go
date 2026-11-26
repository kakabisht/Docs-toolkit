// Package search is a small local full-text index with BM25 ranking. It
// stands in for a site's own search when testing findability offline: the
// results show what a reasonable keyword search can find, not what a
// particular site search engine returns.
package search

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/kakabisht/Docs-toolkit/internal/model"
)

// Field weights: a term in the title counts as much as three in the body.
const (
	wTitle   = 3
	wHeading = 2
	wDesc    = 2
	wTags    = 2
	wBody    = 1
	wCode    = 0.5
	k1       = 1.2
	bParam   = 0.75
)

// Index is a BM25 index over pages.
type Index struct {
	docs  []doc
	df    map[string]int
	avgdl float64
}

type doc struct {
	key, title string
	tf         map[string]float64
	len        float64
}

// Hit is one search result.
type Hit struct {
	Key   string  `json:"key"`
	Title string  `json:"title"`
	Score float64 `json:"score"`
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}_'.-]*[\p{L}\p{N}]|[\p{L}\p{N}]`)

var stop = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an and are as at be by can do does for from how i in into is it its my
of on or our so that the their then this to was we what when where which who why will with you your`) {
		stop[w] = true
	}
}

// Tokens splits text into lower-case, lightly stemmed terms without stop words.
func Tokens(s string) []string {
	var out []string
	for _, w := range wordRe.FindAllString(strings.ToLower(s), -1) {
		w = strings.Trim(w, ".'-")
		if w == "" || stop[w] {
			continue
		}
		out = append(out, Stem(w))
		// dotted and hyphenated names also match their parts
		if strings.ContainsAny(w, ".-_") {
			for _, part := range strings.FieldsFunc(w, func(r rune) bool { return r == '.' || r == '-' || r == '_' }) {
				if part != "" && !stop[part] {
					out = append(out, Stem(part))
				}
			}
		}
	}
	return out
}

// Stem strips common English suffixes. It is deliberately light: plural,
// -ing, -ed, -ly, and -ation/-ise/-ize variants, so "configuring",
// "configured" and "configuration" meet at "configur".
func Stem(w string) string {
	if len(w) <= 3 || strings.ContainsAny(w, "0123456789") {
		return w
	}
	for _, s := range []struct{ suf, rep string }{
		{"ational", "ate"}, {"ations", ""}, {"ation", ""}, {"isation", ""}, {"ization", ""},
		{"ising", ""}, {"izing", ""}, {"ised", ""}, {"ized", ""}, {"ises", ""}, {"izes", ""}, {"ise", ""}, {"ize", ""},
		{"ies", "y"}, {"ing", ""}, {"ed", ""}, {"ly", ""}, {"es", ""}, {"s", ""},
	} {
		if strings.HasSuffix(w, s.suf) && len(w)-len(s.suf)+len(s.rep) >= 3 {
			if s.suf == "s" && (strings.HasSuffix(w, "ss") || strings.HasSuffix(w, "us") || strings.HasSuffix(w, "is")) {
				return w
			}
			if s.suf == "es" && !(strings.HasSuffix(w, "sses") || strings.HasSuffix(w, "xes") || strings.HasSuffix(w, "ches") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "zes")) {
				return strings.TrimSuffix(w, "s")
			}
			w = strings.TrimSuffix(w, s.suf) + s.rep
			break
		}
	}
	// configur(e) and configur meet
	w = strings.TrimSuffix(w, "e")
	if n := len(w); n > 3 && w[n-1] == w[n-2] && !strings.ContainsRune("lsz", rune(w[n-1])) {
		w = w[:n-1]
	}
	return w
}

// Build indexes pages (skipping failed ones).
func Build(pages []*model.Page) *Index {
	ix := &Index{df: map[string]int{}}
	var total float64
	for _, p := range pages {
		if p.Err != "" {
			continue
		}
		tf := map[string]float64{}
		add := func(text string, w float64) {
			for _, t := range Tokens(text) {
				tf[t] += w
			}
		}
		add(p.Title, wTitle)
		for _, h := range p.Headings {
			add(h.Text, wHeading)
		}
		add(p.Description, wDesc)
		add(strings.Join(p.Tags, " "), wTags)
		for _, para := range p.Paragraphs {
			add(para.Text, wBody)
		}
		for _, c := range p.InlineCode {
			add(c.Text, wBody)
		}
		// error messages and commands live in code blocks, and readers paste them
		for _, c := range p.CodeBlocks {
			add(c.Content, wCode)
		}
		// definition terms and first table cells name what reference pages define
		for _, t := range p.DefTerms {
			add(t.Text, wHeading)
		}
		for _, t := range p.TableCells {
			add(t.Text, wBody)
		}
		var l float64
		for t, c := range tf {
			ix.df[t]++
			l += c
		}
		total += l
		ix.docs = append(ix.docs, doc{p.Key, p.Title, tf, l})
	}
	sort.Slice(ix.docs, func(i, j int) bool { return ix.docs[i].key < ix.docs[j].key })
	if len(ix.docs) > 0 {
		ix.avgdl = total / float64(len(ix.docs))
	}
	return ix
}

// Len is the number of indexed pages.
func (ix *Index) Len() int { return len(ix.docs) }

// Search returns up to n hits for q, best first; ties sort by key.
func (ix *Index) Search(q string, n int) []Hit {
	terms := Tokens(q)
	if len(terms) == 0 || len(ix.docs) == 0 {
		return nil
	}
	N := float64(len(ix.docs))
	var hits []Hit
	for _, d := range ix.docs {
		var s float64
		for _, t := range terms {
			f := d.tf[t]
			if f == 0 {
				continue
			}
			df := float64(ix.df[t])
			idf := math.Log(1 + (N-df+0.5)/(df+0.5))
			s += idf * f * (k1 + 1) / (f + k1*(1-bParam+bParam*d.len/ix.avgdl))
		}
		if s > 0 {
			hits = append(hits, Hit{d.key, d.title, math.Round(s*1000) / 1000})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return hits[i].Key < hits[j].Key
	})
	if n > 0 && len(hits) > n {
		hits = hits[:n]
	}
	return hits
}

// Rank returns the 1-based position of key in the results for q, or 0.
func (ix *Index) Rank(q, key string, depth int) int {
	for i, h := range ix.Search(q, depth) {
		if h.Key == key {
			return i + 1
		}
	}
	return 0
}
